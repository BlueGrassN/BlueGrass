package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"bluegrass/client-api/internal/model"
)

func (h *apiHandler) capabilities(_ *http.Request, _ model.Identity) (any, error) {
	_, resources := h.source.(model.ResourceSource)
	return struct {
		ConnectableNodes          bool     `json:"connectable_nodes"`
		ResourceQueries           bool     `json:"resource_queries"`
		AllOwnedResources         bool     `json:"all_owned_resources"`
		ExpirySummaries           bool     `json:"expiry_summaries"`
		SMSLogin                  bool     `json:"sms_login"`
		AuthoritativeExpectedExit bool     `json:"authoritative_expected_exit"`
		ResourceKinds             []string `json:"resource_kinds"`
		OwnershipCoverage         string   `json:"ownership_coverage"`
	}{ConnectableNodes: true, ResourceQueries: resources, ExpirySummaries: resources,
		ResourceKinds: []string{"subscription", "asset"}, OwnershipCoverage: "conservative"}, nil
}

func (h *apiHandler) resources(r *http.Request, identity model.Identity) (any, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, invalidInput()
	}
	kind := "subscription"
	limit := int64(100)
	var after int64
	for key, values := range query {
		if len(values) != 1 {
			return nil, invalidInput()
		}
		switch key {
		case "kind":
			kind = values[0]
		case "per_page":
			var ok bool
			limit, ok = positiveID(values[0])
			if !ok || limit > 100 {
				return nil, invalidInput()
			}
		case "cursor":
		default:
			return nil, invalidInput()
		}
	}
	if kind != "subscription" && kind != "asset" {
		return nil, invalidInput()
	}
	if values, ok := query["cursor"]; ok {
		cursor := values[0]
		if len(cursor) == 0 || len(cursor) > 64 || strings.ContainsAny(cursor, "\r\n=") {
			return nil, invalidInput()
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil || !strings.HasPrefix(string(raw), kind+":") {
			return nil, invalidInput()
		}
		var valid bool
		after, valid = positiveID(strings.TrimPrefix(string(raw), kind+":"))
		if !valid {
			return nil, invalidInput()
		}
	}
	source, ok := h.source.(model.ResourceSource)
	if !ok {
		return nil, model.Fail("capability_unavailable", http.StatusNotImplemented, "Resource queries unavailable")
	}
	return source.Resources(r.Context(), identity.Customer.ID, kind, after, int(limit))
}

func (h *apiHandler) resource(r *http.Request, identity model.Identity) (any, error) {
	kind := r.PathValue("kind")
	id, valid := positiveID(r.PathValue("id"))
	if !valid || (kind != "subscription" && kind != "asset") || r.URL.RawQuery != "" {
		return nil, invalidInput()
	}
	source, ok := h.source.(model.ResourceSource)
	if !ok {
		return nil, model.Fail("capability_unavailable", http.StatusNotImplemented, "Resource queries unavailable")
	}
	return source.Resource(r.Context(), identity.Customer.ID, kind, id)
}
