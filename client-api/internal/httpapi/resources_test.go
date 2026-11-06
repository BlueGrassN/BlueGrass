package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"bluegrass/client-api/internal/model"
)

func TestNodeContractDoesNotInventExpectedExit(t *testing.T) {
	h := newHandler(t, nil, nil, Config{})
	e := checkResponse(t, serve(h, authorized("GET", "/nodes/7", "")), 200)
	var node map[string]any
	if err := json.Unmarshal(e.Data, &node); err != nil {
		t.Fatal(err)
	}
	value, exists := node["expected_exit_ip"]
	if !exists || value != nil {
		t.Fatal("unproven egress must be explicit null")
	}
}

type resourceFixture struct {
	fakeSource
	owner, after int64
	limit        int
	kind         string
	calls        int
}

func (f *resourceFixture) Resources(_ context.Context, owner int64, kind string, after int64, limit int) (model.ResourcePage, error) {
	f.owner, f.kind, f.after, f.limit = owner, kind, after, limit
	f.calls++
	return model.ResourcePage{Items: []model.Resource{{ID: "11", Kind: kind, Name: "Fixture", Status: "expired", Owned: true}},
		Pagination: model.Pagination{PerPage: limit}, Coverage: model.ResourceCoverage{Kind: kind, Consistency: "per_page", Ownership: "conservative"}}, nil
}

func (f *resourceFixture) Resource(_ context.Context, owner int64, kind string, id int64) (model.Resource, error) {
	f.owner, f.kind, f.after = owner, kind, id
	f.calls++
	return model.Resource{}, model.ErrNotFound
}

func TestResourceHTTPUsesSessionOwnerAndExplicitPartialCoverage(t *testing.T) {
	fixture := &resourceFixture{}
	h, err := New(&fakeAuth{}, fixture, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/resources?kind=asset&cursor=YXNzZXQ6Nw&per_page=2", "/resources/expiry?kind=asset&cursor=YXNzZXQ6Nw&per_page=2"} {
		e := checkResponse(t, serve(h, authorized("GET", path, "")), 200)
		if fixture.owner != 41 || fixture.after != 7 || fixture.limit != 2 || fixture.kind != "asset" {
			t.Fatal("request did not bind authenticated owner/kind/cursor/limit")
		}
		var page model.ResourcePage
		if err := json.Unmarshal(e.Data, &page); err != nil {
			t.Fatal(err)
		}
		if page.Coverage.SnapshotComplete || page.Coverage.Ownership != "conservative" || len(page.Items) != 1 {
			t.Fatal("coverage misrepresented")
		}
		for _, private := range []string{"password", "username", "host", "port"} {
			if strings.Contains(string(e.Data), `"`+private+`"`) {
				t.Fatal("private connection data in resource summary")
			}
		}
	}
	for _, kind := range []string{"asset", "subscription"} {
		checkResponse(t, serve(h, authorized("GET", "/resources/"+kind+"/11", "")), 404)
		if fixture.owner != 41 || fixture.kind != kind || fixture.after != 11 {
			t.Fatal("detail ownership not bound")
		}
	}
	e := checkResponse(t, serve(h, authorized("GET", "/capabilities", "")), 200)
	var caps map[string]any
	if err := json.Unmarshal(e.Data, &caps); err != nil {
		t.Fatal(err)
	}
	if caps["resource_queries"] != true || caps["expiry_summaries"] != true || caps["all_owned_resources"] != false || caps["authoritative_expected_exit"] != false {
		t.Fatal("capabilities overclaim source completeness")
	}
}

func TestResourceHTTPRejectsInvalidAndOwnerOverrideQueriesBeforeRead(t *testing.T) {
	fixture := &resourceFixture{}
	h, err := New(&fakeAuth{}, fixture, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/resources?customer_id=99", "/resources?per_page=0", "/resources?per_page=101", "/resources?kind=unknown",
		"/resources?per_page=2&per_page=3", "/resources?kind=asset&kind=subscription", "/resources?cursor=",
		"/resources?kind=subscription&cursor=YXNzZXQ6Nw", "/resources?kind=asset&cursor=YXNzZXQ6Nw==",
		"/resources?kind=asset&cursor=YXNzZXQ6MA", "/resources?kind=asset&cursor=YXNzZXQ6Nw%0A",
		"/resources/asset/0", "/resources/other/11", "/resources/subscription/11?customer_id=99",
	} {
		t.Run(path, func(t *testing.T) {
			checkResponse(t, serve(h, authorized("GET", path, "")), http.StatusUnprocessableEntity)
		})
	}
	if fixture.calls != 0 {
		t.Fatal("invalid request reached source")
	}
}

func TestLegacySourceCannotPretendToHaveResourceCoverage(t *testing.T) {
	h := newHandler(t, nil, nil, Config{})
	for _, path := range []string{"/resources", "/resources/expiry", "/resources/asset/1"} {
		checkResponse(t, serve(h, authorized("GET", path, "")), http.StatusNotImplemented)
	}
}

func TestResourceRoutesRequireAuthentication(t *testing.T) {
	h := newHandler(t, nil, nil, Config{})
	for _, path := range []string{"/capabilities", "/resources", "/resources/expiry", "/resources/subscription/7", "/resources/asset/7"} {
		t.Run(path, func(t *testing.T) { checkResponse(t, serve(h, request("GET", path, "")), 401) })
	}
}
