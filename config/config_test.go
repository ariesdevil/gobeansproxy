package config

import (
	"path"
	"runtime"
	"testing"

	"github.intra.douban.com/coresys/gobeansproxy/util"
)

func getCurrentDir() (dir string, ok bool) {
	if _, filename, _, ok := runtime.Caller(1); ok {
		return path.Dir(filename), ok
	}
	return
}

func TestLoadConfig(t *testing.T) {
	currDir, ok := getCurrentDir()
	if !ok {
		t.Fatal("get current dir error")
	}
	confdir := path.Join(currDir, "../conf")

	proxyCfg := new(ProxyConfig)
	proxyCfg.Load(confdir)

	util.AssertEqual(t, proxyCfg.Hostname, "127.0.0.1", "")
	util.AssertEqual(t, proxyCfg.Port, 7905, "")
	util.AssertEqual(t, proxyCfg.N, 3, "")
	util.AssertEqual(t, proxyCfg.MaxKeyLen, 250, "")
}