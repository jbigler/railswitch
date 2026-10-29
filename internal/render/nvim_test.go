package render

import (
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

// D54: the nvim service gets its config repo and ref as env, and the repo's
// host is added to its firewall's extra hosts.
func TestNvimEnvAndFirewall(t *testing.T) {
	_, in := filial(t, nil)
	if e, ok := envVar(in, "nvim", "NVIM_CONFIG_REPO"); !ok || e.Value != "https://github.com/[ORG]/nvim-config.git" || e.Layer != LayerProject || e.Source != "tools.nvim" {
		t.Errorf("nvim NVIM_CONFIG_REPO = %+v, %v", e, ok)
	}
	if e, ok := envVar(in, "nvim", "NVIM_CONFIG_REF"); ok {
		t.Errorf("nvim NVIM_CONFIG_REF without config_ref = %+v", e)
	}
	if e, ok := envVar(in, "rails", "NVIM_CONFIG_REPO"); ok {
		t.Errorf("rails gets NVIM_CONFIG_REPO = %+v", e)
	}
	_, units := renderUnits(t, fixtureDir, nil)
	hasLine(t, units, "filial-master-nvim.container", "Annotation=filial.firewall.extra="+nvimHosts+",github.com")

	gitlab := func(p *config.Project) {
		p.Tools.Nvim = &config.NvimTool{ConfigRepo: "git@gitlab.com:u/c.git", ConfigRef: "v2"}
	}
	_, in = filial(t, gitlab)
	if e, _ := envVar(in, "nvim", "NVIM_CONFIG_REF"); e.Value != "v2" {
		t.Errorf("nvim NVIM_CONFIG_REF = %+v", e)
	}
	_, units = renderUnits(t, fixtureDir, gitlab)
	hasLine(t, units, "filial-master-nvim.container", "Annotation=filial.firewall.extra="+nvimHosts+",gitlab.com")

	// No config repo: no env, only the tool's own hosts.
	_, units = renderUnits(t, fixtureDir, func(p *config.Project) { p.Tools.Nvim = &config.NvimTool{} })
	hasLine(t, units, "filial-master-nvim.container", "Annotation=filial.firewall.extra="+nvimHosts)
}

// nvimHosts are the nvim tool's own allow hosts (tool.toml allow, D119).
const nvimHosts = "api.github.com,raw.githubusercontent.com,pypi.org,files.pythonhosted.org"
