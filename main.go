package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/256dpi/newdns"
	"github.com/miekg/dns"
)

// --- Updated Structural Schema Definitions ---

type ConfigFile struct {
	Host          string       `json:"host"`
	ProxyFallback *bool        `json:"proxy_fallback"` // Use pointer to recognize explicit false vs missing value
	UpstreamDNS   string       `json:"upstream_dns"`
	Zones         []ZoneConfig `json:"zones"`
}

type ZoneConfig struct {
	ZoneName string               `json:"zone_name"`
	MasterNS string               `json:"master_ns"`
	Records  map[string][]JSONSet `json:"records"`
}

type JSONSet struct {
	Type   string       `json:"type"`
	TTL    string       `json:"ttl"`
	Values []JSONRecord `json:"values"`
}

type JSONRecord struct {
	Address  string   `json:"address,omitempty"`
	Priority int      `json:"priority,omitempty"`
	Weight   int      `json:"weight,omitempty"`
	Port     int      `json:"port,omitempty"`
	Data     []string `json:"data,omitempty"`
}

func parseType(t string) newdns.Type {
	switch strings.ToUpper(t) {
	case "A":
		return newdns.A
	case "AAAA":
		return newdns.AAAA
	case "CNAME":
		return newdns.CNAME
	case "MX":
		return newdns.MX
	case "TXT":
		return newdns.TXT
	case "NS":
		return newdns.NS
	case "SRV":
		return newdns.SRV
	default:
		return newdns.A
	}
}

// --- Multi-Zone Engine Storage with Proxy States ---

type EngineState struct {
	mutex         sync.RWMutex
	bindHost      string
	proxyFallback bool
	upstreamDNS   string
	zoneMap       map[string]*newdns.Zone
	lookupMap     map[string]map[string][]newdns.Set
}

var state = &EngineState{
	proxyFallback: true,         // Default true
	upstreamDNS:   "1.1.1.1:53", // Default fallback server
	zoneMap:       make(map[string]*newdns.Zone),
	lookupMap:     make(map[string]map[string][]newdns.Set),
}

func parseConfiguration(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}

	var cfg ConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("json parsing error: %w", err)
	}

	host := cfg.Host
	if host == "" {
		host = "0.0.0.0:53"
	}

	proxyEnabled := true
	if cfg.ProxyFallback != nil {
		proxyEnabled = *cfg.ProxyFallback
	}

	upstream := cfg.UpstreamDNS
	if upstream == "" {
		upstream = "1.1.1.1:53"
	}

	localZoneMap := make(map[string]*newdns.Zone)
	localLookupMap := make(map[string]map[string][]newdns.Set)

	for _, zc := range cfg.Zones {
		zoneName := zc.ZoneName
		if !strings.HasSuffix(zoneName, ".") {
			zoneName += "."
		}

		subLookup := make(map[string][]newdns.Set)

		for subKey, jsonSets := range zc.Records {
			var engineSets []newdns.Set
			for _, js := range jsonSets {
				var fqdn string
				if subKey == "" {
					fqdn = zoneName
				} else {
					fqdn = fmt.Sprintf("%s.%s", subKey, zoneName)
				}

				ttl := 5 * time.Minute
				if js.TTL != "" {
					if d, err := time.ParseDuration(js.TTL); err == nil {
						ttl = d
					}
				}

				var engineRecords []newdns.Record
				for _, jr := range js.Values {
					engineRecords = append(engineRecords, newdns.Record{
						Address:  jr.Address,
						Priority: jr.Priority,
						Weight:   jr.Weight,
						Port:     jr.Port,
						Data:     jr.Data,
					})
				}

				engineSets = append(engineSets, newdns.Set{
					Name:    fqdn,
					Type:    parseType(js.Type),
					Records: engineRecords,
					TTL:     ttl,
				})
			}
			subLookup[subKey] = engineSets
		}

		localLookupMap[zoneName] = subLookup

		zNameRef := zoneName
		localZoneMap[zoneName] = &newdns.Zone{
			Name:             zoneName,
			MasterNameServer: zc.MasterNS,
			AllNameServers:   []string{zc.MasterNS},
			Handler: func(name string) ([]newdns.Set, error) {
				state.mutex.RLock()
				defer state.mutex.RUnlock()

				if contextZone, exists := state.lookupMap[zNameRef]; exists {
					if sets, found := contextZone[name]; found {
						return sets, nil
					}
				}
				return nil, nil
			},
		}
	}

	state.mutex.Lock()
	state.bindHost = host
	state.proxyFallback = proxyEnabled
	state.upstreamDNS = upstream
	state.zoneMap = localZoneMap
	state.lookupMap = localLookupMap
	state.mutex.Unlock()

	return host, nil
}

type proxyRouter struct {
	authoritative dns.Handler
	logger        newdns.Logger
}

func (r proxyRouter) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) != 1 {
		r.authoritative.ServeDNS(w, req)
		return
	}

	question := req.Question[0]
	if question.Qclass != dns.ClassINET || question.Qtype == dns.TypeANY {
		r.authoritative.ServeDNS(w, req)
		return
	}

	name := newdns.NormalizeDomain(question.Name, true, false, false)

	state.mutex.RLock()
	proxyEnabled := state.proxyFallback
	upstream := state.upstreamDNS
	zones := state.zoneMap

	isLocal := false
	for zoneName := range zones {
		if newdns.InZone(zoneName, name) {
			isLocal = true
			break
		}
	}
	state.mutex.RUnlock()

	if isLocal || !proxyEnabled {
		r.authoritative.ServeDNS(w, req)
		return
	}

	newdns.Proxy(upstream, r.logger).ServeDNS(w, req)
}

func main() {
	const configFile = "config.json"

	initialHost, err := parseConfiguration(configFile)
	if err != nil {
		fmt.Printf("Fatal Configuration Setup Crash: %v\n", err)
		os.Exit(1)
	}

	logger := func(e newdns.Event, msg *dns.Msg, err error, reason string) {
		fmt.Printf("[DNS] Event: %s | Error: %v | Reason: %s\n", e, err, reason)
	}

	server := newdns.NewServer(newdns.Config{
		Handler: func(name string) (*newdns.Zone, error) {
			state.mutex.RLock()
			zones := state.zoneMap
			state.mutex.RUnlock()

			for zoneName, zonePointer := range zones {
				if newdns.InZone(zoneName, name) {
					return zonePointer, nil
				}
			}

			return nil, nil
		},
		Logger: logger,
	})

	go func() {
		fmt.Printf("Dynamic hybrid private/proxy engine listening on %s...\n", initialHost)
		handler := proxyRouter{
			authoritative: server,
			logger:        logger,
		}
		if err := newdns.Run(initialHost, handler, newdns.Accept(logger), nil); err != nil {
			panic(err)
		}
	}()

	fmt.Println("\nServer engine online. Type 'reload' anytime to apply configurations.")
	for {
		var input string
		fmt.Scanln(&input)
		if strings.ToLower(input) == "reload" {
			_, err := parseConfiguration(configFile)
			if err != nil {
				fmt.Printf("⚠️ Reload Failed: %v\n", err)
			} else {
				fmt.Println("🚀 Configuration and Proxy parameters hot-swapped successfully!")
			}
		}
	}
}
