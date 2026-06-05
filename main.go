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

// forwardQuery talks directly to the public web to look up records on behalf of local clients
func forwardQuery(name string, qType uint16) ([]newdns.Set, error) {
	c := dns.Client{Timeout: 3 * time.Second}
	m := dns.Msg{}
	m.SetQuestion(name, qType)
	m.RecursionDesired = true

	state.mutex.RLock()
	upstream := state.upstreamDNS
	state.mutex.RUnlock()

	r, _, err := c.Exchange(&m, upstream)
	if err != nil {
		return nil, err
	}

	if r.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("upstream returned rcode: %d", r.Rcode)
	}

	var resolvedSets []newdns.Set

	// Convert external miekg/dns records back into a format newdns understands
	for _, ans := range r.Answer {
		header := ans.Header()

		// Ensure types match what was requested
		if header.Rrtype != qType {
			continue
		}

		var address string
		var data []string

		switch rr := ans.(type) {
		case *dns.A:
			address = rr.A.String()
		case *dns.AAAA:
			address = rr.AAAA.String()
		case *dns.CNAME:
			address = rr.Target
		case *dns.TXT:
			data = rr.Txt
		case *dns.MX:
			address = rr.Mx
		}

		resolvedSets = append(resolvedSets, newdns.Set{
			Name: header.Name,
			Type: newdns.Type(header.Rrtype),
			TTL:  time.Duration(header.Ttl) * time.Second,
			Records: []newdns.Record{
				{
					Address: address,
					Data:    data,
				},
			},
		})
	}

	return resolvedSets, nil
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

func main() {
	const configFile = "config.json"

	initialHost, err := parseConfiguration(configFile)
	if err != nil {
		fmt.Printf("Fatal Configuration Setup Crash: %v\n", err)
		os.Exit(1)
	}

	server := newdns.NewServer(newdns.Config{
		Handler: func(name string) (*newdns.Zone, error) {
			state.mutex.RLock()
			isProxyEnabled := state.proxyFallback
			zones := state.zoneMap
			state.mutex.RUnlock()

			// 1. If it matches a locally configured zone, handle it locally
			for zoneName, zonePointer := range zones {
				if newdns.InZone(zoneName, name) {
					return zonePointer, nil
				}
			}

			// 2. If it's a completely foreign domain (e.g., github.com) and proxying is allowed:
			if isProxyEnabled {
				return &newdns.Zone{
					Name:             name,
					MasterNameServer: "ns1.proxy.",
					AllNameServers:   []string{"ns1.proxy."},
					Handler: func(relName string) ([]newdns.Set, error) {
						// For proxies, query the incoming FQDN name against upstream public systems
						// We pass the raw type under inspection down the chain
						return forwardQuery(name, dns.TypeA)
					},
				}, nil
			}

			return nil, nil
		},
		Logger: func(e newdns.Event, msg *dns.Msg, err error, reason string) {
			fmt.Printf("[DNS] Event: %s | Error: %v | Reason: %s\n", e, err, reason)
		},
	})

	go func() {
		fmt.Printf("Dynamic hybrid private/proxy engine listening on %s...\n", initialHost)
		if err := server.Run(initialHost); err != nil {
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
