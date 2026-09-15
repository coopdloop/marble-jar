package httpapi

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestQueryCSV(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name   string
		values []string
		want   []string
	}{
		{"absent", nil, nil},
		{"single", []string{"failed"}, []string{"failed"}},
		{"comma separated", []string{"failed,dead_lettered,retrying"},
			[]string{"failed", "dead_lettered", "retrying"}},
		{"repeated param", []string{"failed", "dead_lettered"}, []string{"failed", "dead_lettered"}},
		{"mixed", []string{"failed,retrying", "dead_lettered"},
			[]string{"failed", "retrying", "dead_lettered"}},
		{"padded", []string{" failed , dead_lettered "}, []string{"failed", "dead_lettered"}},
		{"blanks dropped", []string{",,failed,", ""}, []string{"failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			q := url.Values{}
			for _, v := range tc.values {
				q.Add("status", v)
			}
			c.Request = httptest.NewRequest("GET", "/v1/dispatches?"+q.Encode(), nil)
			if got := queryCSV(c, "status"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("queryCSV(%v) = %#v, want %#v", tc.values, got, tc.want)
			}
		})
	}
}

func TestDispatchStatusFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name    string
		query   string
		want    []string
		wantErr string
	}{
		{"unset", "", nil, ""},
		{"one", "status=dead_lettered", []string{"dead_lettered"}, ""},
		{"triage set", "status=failed,dead_lettered", []string{"failed", "dead_lettered"}, ""},
		{"every state", "status=pending,running,succeeded,failed,retrying,dead_lettered",
			[]string{"pending", "running", "succeeded", "failed", "retrying", "dead_lettered"}, ""},
		{"unknown value", "status=complete", nil, "invalid status: complete"},
		{"unknown among known", "status=failed,archived", nil, "invalid status: archived"},
		{"oversized", "status=failed,failed,failed,failed,failed,failed,failed", nil,
			"too many status values (max 6)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/v1/dispatches?"+tc.query, nil)

			got, err := dispatchStatusFilter(c)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("dispatchStatusFilter() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
