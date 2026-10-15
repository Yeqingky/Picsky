package clientip

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestResolveUsesSocketPeerUnlessHeaderSelected(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.80, 10.0.0.2")
	request.Header.Set("X-Real-IP", "203.0.113.81")

	if got := Resolve(request, ""); got != "198.51.100.10" {
		t.Fatalf("默认来源应为 socket peer, 得到 %q", got)
	}
	if got := Resolve(request, "HTTP_X_FORWARDED_FOR"); got != "203.0.113.80" {
		t.Fatalf("应使用选中的头部首个 IP, 得到 %q", got)
	}
	if got := Resolve(request, "HTTP_X_REAL_IP"); got != "203.0.113.81" {
		t.Fatalf("应按配置选择请求头, 得到 %q", got)
	}
}

func TestResolveFallsBackToSocketPeerForMissingOrInvalidHeader(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "[2001:db8::2]:443"
	request.Header.Set("X-Forwarded-For", "unknown, not-an-ip")

	for _, source := range []string{"HTTP_X_FORWARDED_FOR", "HTTP_INVALID!NAME"} {
		if got := Resolve(request, source); got != "2001:db8::2" {
			t.Errorf("来源 %q 无有效地址时应回退 socket peer, 得到 %q", source, got)
		}
	}
}

func TestCandidatesListsOnlyHeadersContainingIPs(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "192.0.2.5:5432"
	request.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	request.Header.Set("X-Real-IP", "[2001:db8::10]")
	request.Header.Set("X-Empty", "not an IP")
	request.Header.Set("Forwarded", `for="198.51.100.2:8080";proto=https`)

	got := Candidates(request)
	want := []Source{
		{Source: RemoteAddrSource, IP: "192.0.2.5"},
		{Source: "HTTP_FORWARDED", IP: "198.51.100.2"},
		{Source: "HTTP_X_FORWARDED_FOR", IP: "203.0.113.10"},
		{Source: "HTTP_X_REAL_IP", IP: "2001:db8::10"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("候选 IP 来源不正确:\n得到: %#v\n期望: %#v", got, want)
	}
}

func TestValidSourceAcceptsOnlySocketOrHeaderSourceNames(t *testing.T) {
	for _, source := range []string{"", RemoteAddrSource, "HTTP_X_FORWARDED_FOR", "HTTP_X_REAL_IP_2"} {
		if !ValidSource(source) {
			t.Errorf("应接受来源 %q", source)
		}
	}
	for _, source := range []string{"X-Forwarded-For", "HTTP_", "HTTP_X-FORWARDED-FOR", "REMOTE_ADDR_EXTRA"} {
		if ValidSource(source) {
			t.Errorf("不应接受来源 %q", source)
		}
	}
}
