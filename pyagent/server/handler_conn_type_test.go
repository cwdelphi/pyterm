package server

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// TestClassifyConnType 与浏览器 parseConnType 规则对齐的判定表
func TestClassifyConnType(t *testing.T) {
	cases := []struct {
		name   string
		local  webrtc.ICECandidateType
		remote webrtc.ICECandidateType
		want   string
	}{
		{"host-host", webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeHost, "P2P"},
		{"srflx-srflx", webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypeSrflx, "P2P"},
		{"host-srflx", webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeSrflx, "P2P"},
		{"prflx-host", webrtc.ICECandidateTypePrflx, webrtc.ICECandidateTypeHost, "P2P"},
		{"relay-host", webrtc.ICECandidateTypeRelay, webrtc.ICECandidateTypeHost, "relay"},
		{"srflx-relay", webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypeRelay, "relay"},
		{"relay-relay", webrtc.ICECandidateTypeRelay, webrtc.ICECandidateTypeRelay, "relay"},
		{"unknown-host", webrtc.ICECandidateTypeUnknown, webrtc.ICECandidateTypeHost, "BUG"},
		{"host-unknown", webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeUnknown, "BUG"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyConnType(c.local, c.remote); got != c.want {
				t.Errorf("classifyConnType(%s, %s) = %q, want %q", c.local, c.remote, got, c.want)
			}
		})
	}
}
