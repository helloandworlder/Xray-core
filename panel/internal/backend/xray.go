package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/xtls/xray-core/panel/pkg/agentrpc"
	"google.golang.org/grpc"
)

func (a *App) buildXrayConfig(ctx context.Context) (map[string]any, error) {
	node, err := a.defaultNode()
	if err != nil {
		return nil, err
	}
	return a.buildXrayConfigForNode(ctx, node.ID)
}

func (a *App) buildXrayConfigForNode(ctx context.Context, nodeID uint) (map[string]any, error) {
	_ = ctx
	inbounds := make([]Inbound, 0)
	if err := a.DB.Where("node_id = ?", nodeID).Order("id asc").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	inboundIDs := make([]uint, 0, len(inbounds))
	for _, ib := range inbounds {
		inboundIDs = append(inboundIDs, ib.ID)
	}
	entryMap := make(map[uint][]InboundEntry, len(inbounds))
	if len(inboundIDs) > 0 {
		entries := make([]InboundEntry, 0)
		if err := a.DB.Where("inbound_id IN ?", inboundIDs).Order("id asc").Find(&entries).Error; err != nil {
			return nil, err
		}
		for _, entry := range entries {
			entryMap[entry.InboundID] = append(entryMap[entry.InboundID], entry)
		}
	}

	orders := make([]Order, 0)
	if err := a.DB.
		Joins("JOIN inbounds ON inbounds.id = orders.inbound_id").
		Where("orders.status = ? AND inbounds.node_id = ?", "active", nodeID).
		Order("orders.id asc").
		Preload("Inbound").
		Preload("Credential").
		Find(&orders).Error; err != nil {
		return nil, err
	}

	ibList := make([]map[string]any, 0, len(inbounds))
	for _, ib := range inbounds {
		proto := strings.ToLower(ib.Protocol)
		clients := make([]map[string]any, 0)
		accounts := make([]map[string]any, 0)
		for _, o := range orders {
			if o.InboundID != ib.ID {
				continue
			}
			switch proto {
			case "vmess":
				clients = append(clients, map[string]any{"id": o.Credential.UUID, "alterId": 0, "email": o.Credential.Email})
			case "vless":
				clients = append(clients, map[string]any{"id": o.Credential.UUID, "email": o.Credential.Email})
			case "mixed":
				username := strings.Split(o.Credential.Email, "@")[0]
				accounts = append(accounts, map[string]any{"user": username, "pass": o.Credential.Password})
			case "ss", "shadowsocks":
				if len(clients) == 0 {
					clients = append(clients, map[string]any{"method": o.Credential.Cipher, "password": o.Credential.Password, "email": o.Credential.Email})
				}
			}
		}

		settings := map[string]any{}
		switch proto {
		case "vmess":
			settings["clients"] = clients
		case "vless":
			settings["decryption"] = "none"
			settings["clients"] = clients
		case "mixed":
			if len(accounts) == 0 {
				settings["auth"] = "noauth"
			} else {
				settings["auth"] = "password"
				settings["accounts"] = accounts
			}
			settings["udp"] = true
		case "ss", "shadowsocks":
			if len(clients) > 0 {
				settings = clients[0]
			}
		default:
			continue
		}

		baseTag := ib.Tag
		if baseTag == "" {
			baseTag = fmt.Sprintf("%s-%d", proto, ib.ID)
		}
		bindTargets := inboundBindTargets(ib, entryMap[ib.ID])
		for _, target := range bindTargets {
			inboundProtocol := proto
			if proto == "ss" {
				inboundProtocol = "shadowsocks"
			}
			tag := baseTag
			if target.EntryID != 0 {
				tag = fmt.Sprintf("%s-e%d", baseTag, target.EntryID)
			}
			ibList = append(ibList, map[string]any{
				"tag":      tag,
				"listen":   target.ListenIP,
				"port":     target.Port,
				"protocol": inboundProtocol,
				"settings": settings,
			})
		}
	}

	endpoints := make([]SocksEndpoint, 0)
	if err := a.DB.Where("status = ?", "active").Order("id asc").Find(&endpoints).Error; err != nil {
		return nil, err
	}

	obList := make([]map[string]any, 0, len(endpoints)+8)
	outboundTagByEndpointID := make(map[uint]string, len(endpoints))
	for _, ep := range endpoints {
		tag := fmt.Sprintf("socks-%d", ep.ID)
		outboundTagByEndpointID[ep.ID] = tag
		server := map[string]any{
			"address": ep.Host,
			"port":    ep.Port,
		}
		if strings.TrimSpace(ep.Username) != "" {
			server["users"] = []map[string]any{{
				"user": ep.Username,
				"pass": ep.Password,
			}}
		}
		ob := map[string]any{
			"tag":      tag,
			"protocol": "socks",
			"settings": map[string]any{
				"servers": []map[string]any{server},
			},
		}
		obList = append(obList, ob)
	}

	residentialTags := make(map[string]string)
	residentialIPs := make([]string, 0)
	for _, o := range orders {
		if normalizeOrderType(o.OrderType) != OrderTypeResidential {
			continue
		}
		egressIP := strings.TrimSpace(o.EgressIP)
		if egressIP == "" {
			return nil, fmt.Errorf("residential order %s requires egressIp", o.OrderNo)
		}
		if net.ParseIP(egressIP) == nil {
			return nil, fmt.Errorf("residential order %s has invalid egressIp %q", o.OrderNo, egressIP)
		}
		if _, ok := residentialTags[egressIP]; ok {
			continue
		}
		tag := "residential-" + sanitizeTagValue(strings.ReplaceAll(egressIP, ":", "-"))
		residentialTags[egressIP] = tag
		residentialIPs = append(residentialIPs, egressIP)
	}
	if len(residentialIPs) > 0 {
		node, err := a.getNodeByID(nodeID)
		if err != nil {
			return nil, fmt.Errorf("resolve node for residential egress validation failed: %w", err)
		}
		status, err := a.agentStatusForNode(ctx, nodeID, node.AgentAddr)
		if err != nil {
			return nil, fmt.Errorf("validate residential egress IP failed: %w", err)
		}
		allowedIPs := make(map[string]struct{}, len(status.InterfaceIPs))
		for _, ip := range status.InterfaceIPs {
			normalized := strings.TrimSpace(ip)
			if normalized != "" {
				allowedIPs[normalized] = struct{}{}
			}
		}
		for _, egressIP := range residentialIPs {
			if _, ok := allowedIPs[egressIP]; !ok {
				return nil, fmt.Errorf("residential egressIp %s is not host-bound on node %d", egressIP, nodeID)
			}
		}
		sort.Strings(residentialIPs)
		for _, egressIP := range residentialIPs {
			obList = append(obList, map[string]any{
				"tag":         residentialTags[egressIP],
				"protocol":    "freedom",
				"sendThrough": egressIP,
				"settings":    map[string]any{},
			})
		}
	}

	obList = append(obList, map[string]any{"tag": "block", "protocol": "blackhole", "settings": map[string]any{}})

	rules := make([]map[string]any, 0, len(orders)+1)
	for _, o := range orders {
		if o.Credential.Email == "" {
			continue
		}
		orderType := normalizeOrderType(o.OrderType)
		outboundTag := ""
		if orderType == OrderTypeResidential {
			egressIP := strings.TrimSpace(o.EgressIP)
			tag, ok := residentialTags[egressIP]
			if !ok {
				return nil, fmt.Errorf("residential order %s has unresolved egressIp %s", o.OrderNo, egressIP)
			}
			outboundTag = tag
		} else {
			if o.SocksEndpointID == nil {
				continue
			}
			tag, ok := outboundTagByEndpointID[*o.SocksEndpointID]
			if !ok {
				return nil, fmt.Errorf("order %s references unavailable socks endpoint %d", o.OrderNo, *o.SocksEndpointID)
			}
			outboundTag = tag
		}
		routeUser := o.Credential.Email
		if strings.ToLower(o.Inbound.Protocol) == "mixed" {
			routeUser = strings.Split(o.Credential.Email, "@")[0]
		}
		rules = append(rules, map[string]any{
			"type":        "field",
			"user":        []string{routeUser},
			"outboundTag": outboundTag,
		})
	}
	rules = append(rules, failCloseRoutingRule())
	if err := validateRoutingRules(rules); err != nil {
		return nil, err
	}

	return map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  ibList,
		"outbounds": obList,
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules":          rules,
		},
	}, nil
}

func sanitizeTagValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	b := strings.Builder{}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "default"
	}
	return result
}

type inboundBindTarget struct {
	EntryID  uint
	ListenIP string
	Port     int
}

func inboundBindTargets(inbound Inbound, entries []InboundEntry) []inboundBindTarget {
	targets := make([]inboundBindTarget, 0, len(entries)+1)
	seen := make(map[string]struct{}, len(entries)+1)
	appendTarget := func(entryID uint, listenIP string, port int) {
		key := fmt.Sprintf("%s:%d", strings.TrimSpace(listenIP), port)
		if key == ":0" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, inboundBindTarget{EntryID: entryID, ListenIP: strings.TrimSpace(listenIP), Port: port})
	}
	appendTarget(0, inbound.ListenIP, inbound.Port)
	for _, entry := range entries {
		appendTarget(entry.ID, entry.EntryIP, entry.EntryPort)
	}
	return targets
}

func failCloseRoutingRule() map[string]any {
	return map[string]any{
		"type":        "field",
		"network":     "tcp,udp",
		"outboundTag": "block",
	}
}

func validateRoutingRules(rules []map[string]any) error {
	if len(rules) == 0 {
		return fmt.Errorf("routing rules cannot be empty")
	}
	for idx, rule := range rules {
		ruleType := strings.TrimSpace(fmt.Sprint(rule["type"]))
		if ruleType != "field" {
			return fmt.Errorf("routing rule %d has unsupported type %q", idx, ruleType)
		}
		outboundTag := strings.TrimSpace(fmt.Sprint(rule["outboundTag"]))
		if outboundTag == "" {
			return fmt.Errorf("routing rule %d has empty outboundTag", idx)
		}
		if !routingRuleHasEffectiveField(rule) {
			return fmt.Errorf("routing rule %d has no effective fields", idx)
		}
	}
	if !isFailCloseCatchAllRule(rules[len(rules)-1]) {
		return fmt.Errorf("last routing rule must be fail-close catch-all to block")
	}
	return nil
}

func routingRuleHasEffectiveField(rule map[string]any) bool {
	effectiveFields := []string{
		"domain",
		"domains",
		"ip",
		"port",
		"source",
		"sourcePort",
		"network",
		"user",
		"inboundTag",
		"protocol",
		"attrs",
	}
	for _, field := range effectiveFields {
		if value, ok := rule[field]; ok && hasEffectiveRoutingFieldValue(value) {
			return true
		}
	}
	return false
}

func hasEffectiveRoutingFieldValue(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case []string:
		return len(v) > 0
	case []any:
		return len(v) > 0
	case nil:
		return false
	default:
		return true
	}
}

func isFailCloseCatchAllRule(rule map[string]any) bool {
	if strings.TrimSpace(fmt.Sprint(rule["outboundTag"])) != "block" {
		return false
	}
	networkText := strings.ToLower(strings.TrimSpace(fmt.Sprint(rule["network"])))
	if networkText == "" {
		return false
	}
	hasTCP := false
	hasUDP := false
	for _, part := range strings.Split(networkText, ",") {
		normalized := strings.TrimSpace(part)
		if normalized == "tcp" {
			hasTCP = true
		}
		if normalized == "udp" {
			hasUDP = true
		}
	}
	return hasTCP && hasUDP
}

func (a *App) publishToAgent(ctx context.Context, nodeID uint, agentAddr string, config map[string]any, validate, reload bool) (*agentrpc.ApplyConfigResponse, error) {
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	agentAddr = strings.TrimSpace(agentAddr)
	if agentAddr == "" {
		agentAddr = a.AgentAddr
	}
	transportCreds, err := a.transportCredentialsForNode(nodeID, agentAddr)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.DialContext(
		ctx,
		agentAddr,
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(agentrpc.JSONCodec{})),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	cli := agentrpc.NewAgentServiceClient(conn)
	return cli.ApplyConfig(ctx, &agentrpc.ApplyConfigRequest{
		ConfigJSON:       string(b),
		Validate:         validate,
		ReloadAfterApply: reload,
	})
}

func (a *App) agentStatus(ctx context.Context, agentAddr string) (*agentrpc.StatusResponse, error) {
	return a.agentStatusForNode(ctx, 0, agentAddr)
}

func (a *App) agentStatusForNode(ctx context.Context, nodeID uint, agentAddr string) (*agentrpc.StatusResponse, error) {
	agentAddr = strings.TrimSpace(agentAddr)
	if agentAddr == "" {
		agentAddr = a.AgentAddr
	}
	transportCreds, err := a.transportCredentialsForNode(nodeID, agentAddr)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.DialContext(
		ctx,
		agentAddr,
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(agentrpc.JSONCodec{})),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	cli := agentrpc.NewAgentServiceClient(conn)
	return cli.Status(ctx, &agentrpc.StatusRequest{})
}
