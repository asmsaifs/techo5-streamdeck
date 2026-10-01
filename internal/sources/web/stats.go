package web

import (
	"sort"

	"github.com/chromedp/chromedp"
	"github.com/shirou/gopsutil/v4/process"
)

// BrowserInfo describes a running browser, for the devices panel.
type BrowserInfo struct {
	Profile string
	Tabs    int   // windows open in it, parked ones included
	Parked  int   // of those, kept warm with nobody watching
	Memory  int64 // bytes resident, the browser and everything it started; 0 if unknown
}

// Stats lists the running browsers, by profile.
func (m *Manager) Stats() []BrowserInfo {
	m.pmu.Lock()
	parked := map[string]int{}
	for _, t := range m.parked {
		parked[t.profile]++
	}
	m.pmu.Unlock()

	m.mu.Lock()
	var out []BrowserInfo
	pids := map[string]int{}
	for name, b := range m.browsers {
		out = append(out, BrowserInfo{Profile: name, Tabs: b.tabs, Parked: parked[name]})
		if c := chromedp.FromContext(b.ctx); c != nil && c.Browser != nil && c.Browser.Process() != nil {
			pids[name] = c.Browser.Process().Pid
		}
	}
	m.mu.Unlock()
	for i := range out {
		if pid, ok := pids[out[i].Profile]; ok {
			out[i].Memory = tree(int32(pid))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Profile < out[j].Profile })
	return out
}

// tree is the resident memory of a process and all it started.
func tree(pid int32) int64 {
	p, err := process.NewProcess(pid)
	if err != nil {
		return 0
	}
	var sum int64
	var walk func(*process.Process)
	walk = func(p *process.Process) {
		if mi, err := p.MemoryInfo(); err == nil {
			sum += int64(mi.RSS)
		}
		kids, _ := p.Children()
		for _, k := range kids {
			walk(k)
		}
	}
	walk(p)
	return sum
}
