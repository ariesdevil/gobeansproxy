package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"path/filepath"
	"runtime"
	"sync"
	"text/template"
	"time"

	"gopkg.in/yaml.v2"

	"github.intra.douban.com/coresys/gobeansdb/cmem"
	dbcfg "github.intra.douban.com/coresys/gobeansdb/config"
	mc "github.intra.douban.com/coresys/gobeansdb/memcache"
	"github.intra.douban.com/coresys/gobeansdb/utils"

	"github.intra.douban.com/coresys/gobeansproxy/config"
	"github.intra.douban.com/coresys/gobeansproxy/dstore"
)

func handleWebPanic(w http.ResponseWriter) {
	r := recover()
	if r != nil {
		stack := utils.GetStack(2000)
		logger.Errorf("web req panic:%#v, stack:%s", r, stack)
		fmt.Fprintf(w, "\npanic:%#v, stack:%s", r, stack)
	}
}

func handleYaml(w http.ResponseWriter, v interface{}) {
	defer handleWebPanic(w)
	b, err := yaml.Marshal(v)
	if err != nil {
		w.Write([]byte(err.Error()))
	} else {
		w.Write(b)
	}
}

func handleJson(w http.ResponseWriter, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		w.Write([]byte(err.Error()))
	} else {
		w.Write(b)
	}
}

type templateHandler struct {
	once     sync.Once
	filename string
	templ    *template.Template
}

func (t *templateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.once.Do(func() {
		t.templ = template.Must(template.New("base.html").Option("missingkey=error").ParseFiles(
			filepath.Join(proxyConf.StaticDir, t.filename),
			filepath.Join(proxyConf.StaticDir, "templates/base.html")))
	})
	var data map[string]interface{}
	if t.filename == "templates/score.html" {
		data = map[string]interface{}{
			"stats": dstore.GetScheduler().Stats(),
		}
	}
	e := t.templ.Execute(w, data)
	if e != nil {
		logger.Errorf("ServerHTTP filename:%s, error: %s", t.filename, e.Error())
	}
}

func startWeb() {
	http.Handle("/templates/", http.FileServer(http.Dir(proxyConf.StaticDir)))

	http.Handle("/", &templateHandler{filename: "templates/score.html"})
	http.Handle("/score/", &templateHandler{filename: "templates/score.html"})
	http.Handle("/stats/", &templateHandler{filename: "templates/stats.html"})

	http.HandleFunc("/stats/config/", handleConfig)
	http.HandleFunc("/stats/request/", handleRequest)
	http.HandleFunc("/stats/buffer/", handleBuffer)
	http.HandleFunc("/stats/memstat/", handleMemStat)
	http.HandleFunc("/stats/rusage/", handleRusage)
	http.HandleFunc("/stats/score/", handleScore)

	http.HandleFunc("/stats/route/", handleRoute)
	http.HandleFunc("/stats/route/reload", handleRouteReload)

	webaddr := fmt.Sprintf("%s:%d", proxyConf.Listen, proxyConf.WebPort)
	go func() {
		logger.Infof("HTTP listen at %s", webaddr)
		if err := http.ListenAndServe(webaddr, nil); err != nil {
			logger.Fatalf("ListenAndServer: %s", err.Error())
		}
	}()
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	handleJson(w, proxyConf)
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	handleJson(w, mc.RL)
}

func handleRusage(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	rusage := utils.Getrusage()
	handleJson(w, rusage)
}

func handleMemStat(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	handleJson(w, ms)
}

func handleBuffer(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	handleJson(w, &cmem.DBRL)
}

func handleScore(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	scores := dstore.GetScheduler().Stats()
	handleJson(w, scores)
}

func handleRoute(w http.ResponseWriter, r *http.Request) {
	defer handleWebPanic(w)
	handleYaml(w, config.Route)
}

func handleRouteReload(w http.ResponseWriter, r *http.Request) {
	var err error
	if !dbcfg.AllowReload {
		w.Write([]byte("reloading"))
		return
	}

	dbcfg.AllowReload = false
	defer func() {
		dbcfg.AllowReload = true
		if err != nil {
			logger.Errorf("handleRoute err", err.Error())
			w.Write([]byte(fmt.Sprintf(err.Error())))
			return
		}
	}()

	if len(proxyConf.ZKServers) == 0 {
		w.Write([]byte("not using zookeeper"))
		return
	}

	defer handleWebPanic(w)
	newRouteContent, stat, err := dbcfg.ZKClient.GetRouteRaw()
	if err != nil {
		return
	}
	if dbcfg.ZKClient.Stat != nil && stat.Version == dbcfg.ZKClient.Stat.Version {
		w.Write([]byte(fmt.Sprintf("same version %d", stat.Version)))
		return
	}
	info := fmt.Sprintf("update with route version %d\n", stat.Version)
	logger.Infof(info)
	newRoute := new(dbcfg.RouteTable)
	err = newRoute.LoadFromYaml(newRouteContent)
	if err != nil {
		return
	}

	oldScheduler := dstore.GetScheduler()
	dstore.InitGlobalManualScheduler(newRoute, proxyConf.N)
	config.Route = newRoute
	dbcfg.ZKClient.Stat = stat
	w.Write([]byte("success"))

	go func() {
		// sleep for request to be completed.
		time.Sleep(time.Duration(proxyConf.ReadTimeoutMs) * time.Millisecond * 5)
		oldScheduler.Close()
	}()
}
