package relay

import (
	"io"
	"sync"

	"github.com/pion/webrtc/v4"
)

// WebRTCRelay manages a PeerConnection and detached DataChannel for relaying.
type WebRTCRelay struct {
	peerConn   *webrtc.PeerConnection
	dataChan   *webrtc.DataChannel
	dcReader   io.ReadCloser
	dcWriter   io.WriteCloser
	mu         sync.Mutex
	onOpen     func()
	onClose    func()
	onError    func(error)
}

func NewRelay(iceServers []webrtc.ICEServer) (*WebRTCRelay, error) {
	config := webrtc.Configuration{
		ICEServers: iceServers,
	}
	api := webrtc.NewAPI()
	peerConn, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	r := &WebRTCRelay{peerConn: peerConn}
	return r, nil
}

// CreateDataChannel creates a detached data channel for raw I/O.
func (r *WebRTCRelay) CreateDataChannel(label string) error {
	dc, err := r.peerConn.CreateDataChannel(label, &webrtc.DataChannelInit{
		Detach: webrtc.Bool(true),
	})
	if err != nil {
		return err
	}
	r.dataChan = dc
	dc.OnOpen(func() {
	attachment, err := dc.Detach()
		if err != nil {
			if r.onError != nil {
				r.onError(err)
			}
			return
		}
		r.dcReader = attachment
		r.dcWriter = attachment
		if r.onOpen != nil {
			r.onOpen()
		}
	})
	dc.OnClose(func() {
		if r.onClose != nil {
			r.onClose()
		}
	})
	return nil
}

// SetRemoteDescription sets the SDP answer from the agent.
func (r *WebRTCRelay) SetRemoteDescription(sdp webrtc.SessionDescription) error {
	return r.peerConn.SetRemoteDescription(sdp)
}

// CreateAnswer creates an SDP answer.
func (r *WebRTCRelay) CreateAnswer() (*webrtc.SessionDescription, error) {
	answer, err := r.peerConn.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}
	gatherDone := webrtc.GatheringCompletePromise(r.peerConn)
	if err := r.peerConn.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gatherDone
	return r.peerConn.LocalDescription(), nil
}

// AddICECandidate adds a remote ICE candidate.
func (r *WebRTCRelay) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return r.peerConn.AddICECandidate(candidate)
}

// OnLocalDescription is called when a local description is available.
func (r *WebRTCRelay) OnLocalDescription(fn func(sdp webrtc.SessionDescription)) {
	r.peerConn.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
	})
}

// OnICECandidate is called for each local ICE candidate.
func (r *WebRTCRelay) OnICECandidate(fn func(candidate webrtc.ICECandidateInit)) {
	r.peerConn.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		fn(webrtc.ICECandidateInit{
			Candidate:     init.Candidate,
			SDPMid:        init.SDPMid,
			SDPMLineIndex: init.SDPMLineIndex,
		})
	})
}

func (r *WebRTCRelay) OnOpen(fn func())  { r.onOpen = fn }
func (r *WebRTCRelay) OnClose(fn func()) { r.onClose = fn }
func (r *WebRTCRelay) OnError(fn func(error)) { r.onError = fn }

func (r *WebRTCRelay) Reader() io.ReadCloser  { return r.dcReader }
func (r *WebRTCRelay) Writer() io.WriteCloser { return r.dcWriter }

func (r *WebRTCRelay) Close() {
	if r.peerConn != nil {
		r.peerConn.Close()
	}
}
