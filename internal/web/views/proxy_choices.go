package views

import (
	"encoding/json"
	"slices"

	"github.com/y0f/asura/internal/model"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

// Proxy options follow the same transport registry as writes and checks. DNS
// needs its current form value as well as a supported transport.
func proxyChoiceDisabledExpression(protocol string) string {
	types := make([]string, 0)
	for kind := range validate.ValidMonitorTypes {
		monitor := &model.Monitor{Type: kind, Settings: json.RawMessage(`{"server":"dns.example.test"}`)}
		if model.ValidateProxyCompatibility(monitor, protocol) == nil {
			types = append(types, kind)
		}
	}
	slices.Sort(types)
	encoded, _ := json.Marshal(types)
	return "!" + string(encoded) + ".includes(monitorType) || (monitorType === 'dns' && !proxyDNSServer.trim())"
}

func (p MonitorFormParams) proxyChoiceDisabled(proxy *storage.Proxy) bool {
	settings, _ := json.Marshal(model.DNSSettings{Server: p.DNS.Server})
	return model.ValidateProxyCompatibility(&model.Monitor{Type: p.monitorTypeOrDefault(), Settings: settings}, proxy.Protocol) != nil
}
