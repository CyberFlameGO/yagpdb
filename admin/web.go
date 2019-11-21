package admin

import (
	"github.com/jonas747/dshardorchestrator/orchestrator/rest"
	"github.com/jonas747/yagpdb/common/internalapi"
	"io"
	"net/http"
	"strconv"

	"emperror.dev/errors"
	"github.com/jonas747/yagpdb/common"
	"github.com/jonas747/yagpdb/web"
	"goji.io"
	"goji.io/pat"
)

// InitWeb implements web.Plugin
func (p *Plugin) InitWeb() {
	web.LoadHTMLTemplate("../../admin/assets/bot_admin_panel.html", "templates/plugins/bot_admin_panel.html")

	mux := goji.SubMux()
	web.RootMux.Handle(pat.New("/admin/*"), mux)
	web.RootMux.Handle(pat.New("/admin"), mux)

	mux.Use(web.RequireSessionMiddleware)
	mux.Use(web.RequireBotOwnerMW)

	panelHandler := web.ControllerHandler(p.handleGetPanel, "bot_admin_panel")

	mux.Handle(pat.Get(""), panelHandler)
	mux.Handle(pat.Get("/"), panelHandler)

	// Debug routes
	mux.Handle(pat.Get("/host/:host/pid/:pid/goroutines"), p.ProxyGetInternalAPI("/debug/pprof/goroutine"))
	mux.Handle(pat.Get("/host/:host/pid/:pid/trace"), p.ProxyGetInternalAPI("/debug/pprof/trace"))
	mux.Handle(pat.Get("/host/:host/pid/:pid/profile"), p.ProxyGetInternalAPI("/debug/pprof/profile"))
	mux.Handle(pat.Get("/host/:host/pid/:pid/heap"), p.ProxyGetInternalAPI("/debug/pprof/heap"))
	mux.Handle(pat.Get("/host/:host/pid/:pid/allocs"), p.ProxyGetInternalAPI("/debug/pprof/allocs"))

	// Control routes
	mux.Handle(pat.Post("/host/:host/pid/:pid/shutdown"), web.ControllerPostHandler(p.handleShutdown, panelHandler, nil, ""))

	// Orhcestrator controls
	mux.Handle(pat.Post("/host/:host/pid/:pid/updateversion"), web.ControllerPostHandler(p.handleUpgrade, panelHandler, nil, ""))
	mux.Handle(pat.Post("/host/:host/pid/:pid/migratenodes"), web.ControllerPostHandler(p.handleMigrateNodes, panelHandler, nil, ""))
	mux.Handle(pat.Get("/host/:host/pid/:pid/deployedversion"), http.HandlerFunc(p.handleLaunchNodeVersion))
}

func (p *Plugin) handleGetPanel(w http.ResponseWriter, r *http.Request) (web.TemplateData, error) {
	_, tmpl := web.GetBaseCPContextData(r.Context())

	hosts, err := common.ServicePoller.GetActiveServiceHosts()
	if err != nil {
		return tmpl, errors.WithStackIf(err)
	}

	tmpl["ServiceHosts"] = hosts

	return tmpl, nil
}

func (p *Plugin) ProxyGetInternalAPI(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		debug := r.URL.Query().Get("debug")
		debugStr := ""
		if debug != "" {
			debugStr = "?debug=" + debug
		}

		sh, err := findServicehost(r)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Error querying service hosts: " + err.Error()))
			return
		}

		resp, err := http.Get("http://" + sh.InternalAPIAddress + path + debugStr)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Error querying internal api: " + err.Error()))
			return
		}

		io.Copy(w, resp.Body)
	})
}

func (p *Plugin) handleShutdown(w http.ResponseWriter, r *http.Request) (web.TemplateData, error) {
	_, tmpl := web.GetBaseCPContextData(r.Context())

	sh, err := findServicehost(r)
	if err != nil {
		return tmpl, err
	}

	var resp string
	err = internalapi.PostWithAddress(sh.InternalAPIAddress, "shutdown", nil, &resp)
	if err != nil {
		return tmpl, err
	}

	tmpl = tmpl.AddAlerts(web.SucessAlert(resp))
	return tmpl, nil
}

func (p *Plugin) handleUpgrade(w http.ResponseWriter, r *http.Request) (web.TemplateData, error) {
	_, tmpl := web.GetBaseCPContextData(r.Context())

	client, err := createOrhcestatorRESTClient(r)
	if err != nil {
		return tmpl, err
	}

	logger.Println("Upgrading version...")

	newVer, err := client.PullNewVersion()
	if err != nil {
		tmpl.AddAlerts(web.ErrorAlert(err.Error()))
		return tmpl, err
	}

	tmpl = tmpl.AddAlerts(web.SucessAlert("Upgraded to ", newVer))
	return tmpl, nil
}

func (p *Plugin) handleMigrateNodes(w http.ResponseWriter, r *http.Request) (web.TemplateData, error) {
	_, tmpl := web.GetBaseCPContextData(r.Context())

	client, err := createOrhcestatorRESTClient(r)
	if err != nil {
		return tmpl, err
	}

	logger.Println("Upgrading version...")

	response, err := client.MigrateAllNodesToNewNodes()
	if err != nil {
		tmpl.AddAlerts(web.ErrorAlert(err.Error()))
		return tmpl, err
	}

	tmpl = tmpl.AddAlerts(web.SucessAlert(response))
	return tmpl, nil
}

func (p *Plugin) handleLaunchNodeVersion(w http.ResponseWriter, r *http.Request) {
	logger.Println("ahahha")

	client, err := createOrhcestatorRESTClient(r)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error querying service hosts: " + err.Error()))
		return
	}

	ver, err := client.GetDeployedVersion()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error getting deployed version: " + err.Error()))
		return
	}

	w.Write([]byte(ver))
}

func createOrhcestatorRESTClient(r *http.Request) (*rest.Client, error) {
	sh, err := findServicehost(r)
	if err != nil {
		return nil, err
	}

	for _, v := range sh.Services {
		if v.Type == common.ServiceTypeOrchestator {
			return rest.NewClient("http://" + sh.InternalAPIAddress), nil
		}
	}

	return nil, common.ErrNotFound
}

func findServicehost(r *http.Request) (*common.ServiceHost, error) {
	host := pat.Param(r, "host")
	pid := pat.Param(r, "pid")

	serviceHosts, err := common.ServicePoller.GetActiveServiceHosts()
	if err != nil {
		return nil, err
	}

	for _, v := range serviceHosts {
		if v.Host == host && pid == strconv.Itoa(v.PID) {
			return v, nil
		}
	}

	return nil, common.ErrNotFound
}
