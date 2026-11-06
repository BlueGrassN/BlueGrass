package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"bluegrass/client-api/internal/model"
)

type namedResourceSource struct {
	fakeSource
	resource model.Resource
}

func (s *namedResourceSource) Resources(_ context.Context, owner int64, kind string, _ int64, limit int) (model.ResourcePage, error) {
	if owner != 41 {
		return model.ResourcePage{}, model.ErrNotFound
	}
	item := s.resource
	item.Kind = kind
	return model.ResourcePage{Items: []model.Resource{item}, Pagination: model.Pagination{PerPage: limit}}, nil
}

func (s *namedResourceSource) Resource(_ context.Context, owner int64, kind string, id int64) (model.Resource, error) {
	if owner != 41 || id != 11 {
		return model.Resource{}, model.ErrNotFound
	}
	item := s.resource
	item.Kind = kind
	return item, nil
}

func TestHTTPReturnsAssetNameAndCustomerRemarkSeparately(t *testing.T) {
	for _, remark := range []string{"我的办公节点", ""} {
		// Populate through JSON so this regression also runs against the old DTO
		// that silently drops the newly required customer_remark field.
		payload, err := json.Marshal(map[string]any{"id": "11", "name": "美国-198.87.96.137", "customer_remark": remark})
		if err != nil {
			t.Fatal(err)
		}
		var node model.Node
		fixture := &namedResourceSource{}
		if err := json.Unmarshal(payload, &node); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &fixture.resource); err != nil {
			t.Fatal(err)
		}
		fixture.nodes = func(_ context.Context, owner, _ int64, limit int) (model.NodePage, error) {
			if owner != 41 {
				return model.NodePage{}, model.ErrNotFound
			}
			return model.NodePage{Items: []model.Node{node}, Pagination: model.Pagination{PerPage: limit}}, nil
		}
		fixture.node = func(_ context.Context, owner, id int64) (model.Node, error) {
			if owner != 41 || id != 11 {
				return model.Node{}, model.ErrNotFound
			}
			return node, nil
		}
		h, err := New(&fakeAuth{}, fixture, Config{})
		if err != nil {
			t.Fatal(err)
		}
		for _, route := range []struct {
			path string
			list bool
		}{
			{"/nodes", true}, {"/nodes/11", false},
			{"/resources?kind=subscription", true}, {"/resources/subscription/11", false},
			{"/resources/expiry?kind=subscription", true},
			{"/resources?kind=asset", true}, {"/resources/asset/11", false}, {"/resources/expiry?kind=asset", true},
		} {
			if remark != "" && strings.Contains(route.path, "asset") {
				continue // Bare assets have no subscription customer remark.
			}
			t.Run(remark+route.path, func(t *testing.T) {
				e := checkResponse(t, serve(h, authorized("GET", route.path, "")), 200)
				var data map[string]any
				if err := json.Unmarshal(e.Data, &data); err != nil {
					t.Fatal(err)
				}
				if route.list {
					items, ok := data["items"].([]any)
					if !ok || len(items) != 1 {
						t.Fatal("expected one resource")
					}
					data = items[0].(map[string]any)
				}
				if data["name"] != "美国-198.87.96.137" || data["customer_remark"] != remark {
					t.Fatalf("independent name/remark fields lost: name=%v customer_remark=%v", data["name"], data["customer_remark"])
				}
			})
		}
	}
}
