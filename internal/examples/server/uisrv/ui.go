package uisrv

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/internal/examples/certs"
	"github.com/open-telemetry/opamp-go/internal/examples/html"
	"github.com/open-telemetry/opamp-go/internal/examples/server/data"
	"github.com/open-telemetry/opamp-go/protobufs"
)

var (
	htmlDir string
	srv     *http.Server
)

var logger = log.New(log.Default().Writer(), "[UI] ", log.Default().Flags()|log.Lmsgprefix|log.Lmicroseconds)

type fleetPageData struct {
	Summary fleetSummary
	Agents  []fleetAgentRow
}

type fleetSummary struct {
	TotalAgents           int
	HealthyAgents         int
	UnhealthyAgents       int
	AgentsWithConfigError int
}

type fleetAgentRow struct {
	InstanceID         string
	DisplayName        string
	DetailURL          string
	StatusLabel        string
	StatusClass        string
	StatusReason       string
	StartedAt          string
	ServiceName        string
	Environment        string
	HostName           string
	RemoteConfigLabel  string
	RemoteConfigClass  string
	RemoteConfigReason string
	MessageCount       int
}

func Start(rootDir string) {
	htmlDir = path.Join(rootDir, "uisrv/html")

	mux := http.NewServeMux()
	mux.HandleFunc("/", renderRoot)
	mux.HandleFunc("/agent", renderAgent)
	mux.HandleFunc("/save_config", saveCustomConfigForInstance)
	mux.HandleFunc("/rotate_client_cert", rotateInstanceClientCert)
	mux.HandleFunc("/opamp_connection_settings", opampConnectionSettings)
	mux.HandleFunc("/send_custom_message", sendCustomMessage)
	srv = &http.Server{
		Addr:    "0.0.0.0:4321",
		Handler: mux,
	}
	go srv.ListenAndServe()
}

func Shutdown() {
	srv.Shutdown(context.Background())
}

func renderTemplate(w http.ResponseWriter, htmlTemplateFile string, data interface{}) {
	t, err := template.ParseFS(html.HtmlFS, "html/*")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Printf("Error parsing html template %s: %v", htmlTemplateFile, err)
		return
	}

	err = t.Lookup(htmlTemplateFile).Execute(w, data)
	if err != nil {
		// It is too late to send an HTTP status code since content is already written.
		// We can just log the error.
		logger.Printf("Error writing html content %s: %v", htmlTemplateFile, err)
		return
	}
}

func renderRoot(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "root.html", newFleetPageData(data.AllAgents.GetAllAgentsReadonlyClone()))
}

func newFleetPageData(agents map[data.InstanceId]*data.Agent) fleetPageData {
	rows := make([]fleetAgentRow, 0, len(agents))
	summary := fleetSummary{}

	for _, agent := range agents {
		row := newFleetAgentRow(agent)
		rows = append(rows, row)

		summary.TotalAgents++
		if row.StatusClass == "status-healthy" {
			summary.HealthyAgents++
		} else {
			summary.UnhealthyAgents++
		}
		if row.RemoteConfigClass == "status-error" {
			summary.AgentsWithConfigError++
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StatusLabel != rows[j].StatusLabel {
			return rows[i].StatusLabel < rows[j].StatusLabel
		}
		if rows[i].DisplayName != rows[j].DisplayName {
			return rows[i].DisplayName < rows[j].DisplayName
		}
		return rows[i].InstanceID < rows[j].InstanceID
	})

	return fleetPageData{Summary: summary, Agents: rows}
}

func newFleetAgentRow(agent *data.Agent) fleetAgentRow {
	row := fleetAgentRow{
		InstanceID:        agent.InstanceIdStr,
		DisplayName:       agent.InstanceIdStr,
		DetailURL:         "/agent?instanceid=" + agent.InstanceIdStr,
		StatusLabel:       "Unknown",
		StatusClass:       "status-unknown",
		StartedAt:         "—",
		ServiceName:       "—",
		Environment:       "—",
		HostName:          "—",
		RemoteConfigLabel: "Not reported",
		RemoteConfigClass: "status-unknown",
		MessageCount:      len(agent.CustomMessageHistory),
	}

	if agent.Status != nil {
		row.ServiceName = preferredAgentAttribute(agent.Status.AgentDescription,
			"service.name", "service.namespace", "service.instance.id")
		row.Environment = preferredAgentAttribute(agent.Status.AgentDescription,
			"deployment.environment", "service.namespace")
		row.HostName = preferredAgentAttribute(agent.Status.AgentDescription,
			"host.name", "host.id", "k8s.pod.name")
		if row.HostName != "—" {
			row.DisplayName = row.HostName
		}

		if agent.Status.Health != nil {
			if agent.Status.Health.Healthy {
				row.StatusLabel = "Healthy"
				row.StatusClass = "status-healthy"
			} else {
				row.StatusLabel = "Unhealthy"
				row.StatusClass = "status-error"
			}

			if agent.Status.Health.LastError != "" {
				row.StatusReason = agent.Status.Health.LastError
			}
		}

		if !agent.StartedAt.IsZero() {
			row.StartedAt = agent.StartedAt.Format(time.RFC3339)
		}

		if agent.Status.RemoteConfigStatus != nil {
			switch {
			case agent.Status.RemoteConfigStatus.ErrorMessage != "":
				row.RemoteConfigLabel = "Error"
				row.RemoteConfigClass = "status-error"
				row.RemoteConfigReason = agent.Status.RemoteConfigStatus.ErrorMessage
			case len(agent.Status.RemoteConfigStatus.LastRemoteConfigHash) > 0:
				row.RemoteConfigLabel = "Applied"
				row.RemoteConfigClass = "status-healthy"
			default:
				row.RemoteConfigLabel = "Pending"
				row.RemoteConfigClass = "status-warning"
			}
		}
	}

	return row
}

func preferredAgentAttribute(desc *protobufs.AgentDescription, keys ...string) string {
	if desc == nil {
		return "—"
	}

	for _, key := range keys {
		if value := agentAttribute(desc.IdentifyingAttributes, key); value != "" {
			return value
		}
		if value := agentAttribute(desc.NonIdentifyingAttributes, key); value != "" {
			return value
		}
	}

	return "—"
}

func agentAttribute(attrs []*protobufs.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.GetKey() == key {
			value := fmt.Sprint(attr.GetValue())
			value = strings.TrimPrefix(value, "string_value:")
			value = strings.Trim(value, " \t\n\"")
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func renderAgent(w http.ResponseWriter, r *http.Request) {
	uid, err := uuid.Parse(r.URL.Query().Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	agent := data.AllAgents.GetAgentReadonlyClone(data.InstanceId(uid))
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	renderTemplate(w, "agent.html", agent)
}

func sendCustomMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	capability := r.PostForm.Get("capability")
	msgType := r.PostForm.Get("type")
	dataStr := r.PostForm.Get("data")

	customMsg := &protobufs.ServerToAgent{
		CustomMessage: &protobufs.CustomMessage{
			Capability: capability,
			Type:       msgType,
			Data:       []byte(dataStr),
		},
	}

	data.AllAgents.SendCustomMessageToAgent(instanceId, customMsg)

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func saveCustomConfigForInstance(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	configStr := r.PostForm.Get("config")
	config := &protobufs.AgentConfigMap{
		ConfigMap: map[string]*protobufs.AgentConfigFile{
			"": {Body: []byte(configStr)},
		},
	}

	notifyNextStatusUpdate := make(chan struct{}, 1)
	data.AllAgents.SetCustomConfigForAgent(instanceId, config, notifyNextStatusUpdate)

	// Wait for up to 5 seconds for a Status update, which is expected
	// to be reported by the Agent after we set the remote config.
	timer := time.NewTicker(time.Second * 5)

	select {
	case <-notifyNextStatusUpdate:
	case <-timer.C:
	}

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func rotateInstanceClientCert(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Find the agent instance.
	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Create a new certificate for the agent.
	certificate, err := certs.CreateTLSCert(certs.CaCert, certs.CaKey)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		logger.Println(err)
		return
	}

	// Create an offer for the agent.
	offers := &protobufs.ConnectionSettingsOffers{
		Opamp: &protobufs.OpAMPConnectionSettings{
			Certificate: certificate,
		},
	}

	// Send the offer to the agent.
	data.AllAgents.OfferAgentConnectionSettings(instanceId, offers)

	logger.Printf("Waiting for agent %s to reconnect\n", instanceId)

	// Wait for up to 5 seconds for a Status update, which is expected
	// to be reported by the agent after we set the remote config.
	timer := time.NewTicker(time.Second * 5)

	// TODO: wait for agent to reconnect instead of waiting full 5 seconds.

	select {
	case <-timer.C:
		logger.Printf("Time out waiting for agent %s to reconnect\n", instanceId)
	}

	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}

func opampConnectionSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Find the agent instance.
	uid, err := uuid.Parse(r.Form.Get("instanceid"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	instanceId := data.InstanceId(uid)
	agent := data.AllAgents.GetAgentReadonlyClone(instanceId)
	if agent == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// parse tls_min
	tlsMinVal := r.Form.Get("tls_min")
	var tlsMin string
	switch tlsMinVal {
	case "TLSv1.0":
		tlsMin = "1.0"
	case "TLSv1.1":
		tlsMin = "1.1"
	case "TLSv1.2":
		tlsMin = "1.2"
	case "TLSv1.3":
		tlsMin = "1.3"
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	offers := &protobufs.ConnectionSettingsOffers{
		Opamp: &protobufs.OpAMPConnectionSettings{
			Tls: &protobufs.TLSConnectionSettings{
				CaPemContents: string(certs.CaCert),
				MinVersion:    tlsMin,
				MaxVersion:    "1.3",
			},
		},
	}
	proxyURL := r.Form.Get("proxy_url")
	if len(proxyURL) > 0 {
		u, err := url.Parse(proxyURL)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		offers.Opamp.Proxy = &protobufs.ProxyConnectionSettings{
			Url: u.String(),
		}
	}

	data.AllAgents.OfferAgentConnectionSettings(instanceId, offers)
	http.Redirect(w, r, "/agent?instanceid="+uid.String(), http.StatusSeeOther)
}
