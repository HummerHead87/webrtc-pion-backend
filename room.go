package chat_server

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v2"
	"github.com/vektah/gqlparser/gqlerror"
)

type room struct {
	pubReceiver *webrtc.PeerConnection

	// Local track
	videoTrack     *webrtc.Track
	audioTrack     *webrtc.Track
	videoTrackLock sync.RWMutex
	audioTrackLock sync.RWMutex
}

type rooms struct {
	sync.Mutex
	items map[string]*room
}

// Peer config
var peerConnectionConfig = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
			// URLs: []string{"stun:35.184.155.87:3478"},
		},
	},
	SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
}

const (
	rtcpPLIInterval = time.Second * 3
)

var rms = rooms{
	items: map[string]*room{},
}

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}

func (r *mutationResolver) PublishStream(ctx context.Context, user string, sdp string) (string, error) {
	var hasKey bool
	rms.Lock()
	_, hasKey = rms.items[user]
	rms.Unlock()
	if hasKey {
		return "", gqlerror.Errorf("user with name '%s' already streaming. Choose another name", user)
	}

	rm := room{}
	_ctx, cancel := context.WithCancel(context.Background())

	cancelled := func() bool {
		select {
		case <-_ctx.Done():
			return true
		default:
			return false
		}
	}

	// Create a new RTCPeerConnection
	pubReceiver, err := r.api.NewPeerConnection(peerConnectionConfig)
	checkError(err)
	rm.pubReceiver = pubReceiver

	_, err = pubReceiver.AddTransceiver(webrtc.RTPCodecTypeAudio)
	checkError(err)

	_, err = pubReceiver.AddTransceiver(webrtc.RTPCodecTypeVideo)
	checkError(err)

	pubReceiver.OnTrack(func(remoteTrack *webrtc.Track, receiver *webrtc.RTPReceiver) {
		if remoteTrack.PayloadType() == webrtc.DefaultPayloadTypeVP8 ||
			remoteTrack.PayloadType() == webrtc.DefaultPayloadTypeVP9 ||
			remoteTrack.PayloadType() == webrtc.DefaultPayloadTypeH264 {
			// Create a local video track, all our SFU clients will be fed via this track
			var err error
			videoTrack, err := pubReceiver.NewTrack(remoteTrack.PayloadType(), remoteTrack.SSRC(), "video", "pion")
			checkError(err)
			rm.videoTrackLock.Lock()
			rm.videoTrack = videoTrack
			rm.videoTrackLock.Unlock()

			// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
			go func() {
				ticker := time.NewTicker(rtcpPLIInterval)
				/* for range ticker.C {
					checkError(pubReceiver.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: videoTrack.SSRC()}}))
				} */
				for {
					select {
					case <-ticker.C:
						checkError(pubReceiver.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: videoTrack.SSRC()}}))
					case <-_ctx.Done():
						return
					}
				}
			}()

			rtpBuf := make([]byte, 1400)
			for {
				i, err := remoteTrack.Read(rtpBuf)
				if cancelled() {
					break
				}
				checkError(err)
				rm.videoTrackLock.RLock()
				_, err = rm.videoTrack.Write(rtpBuf[:i])
				rm.videoTrackLock.RUnlock()

				if err != io.ErrClosedPipe {
					checkError(err)
				}
			}
		} else {
			// Create a local audio track, all our SFU clients will be fed via this track
			audioTrack, err := pubReceiver.NewTrack(remoteTrack.PayloadType(), remoteTrack.SSRC(), "audio", "pion")
			checkError(err)
			rm.audioTrackLock.Lock()
			rm.audioTrack = audioTrack
			rm.audioTrackLock.Unlock()

			rtpBuf := make([]byte, 1400)
			for {
				i, err := remoteTrack.Read(rtpBuf)
				if cancelled() {
					break
				}
				checkError(err)
				rm.audioTrackLock.RLock()
				_, err = rm.audioTrack.Write(rtpBuf[:i])
				rm.audioTrackLock.RUnlock()
				if err != io.ErrClosedPipe {
					checkError(err)
				}
			}
		}

	})

	pubReceiver.OnSignalingStateChange(func(signalState webrtc.SignalingState) {
		fmt.Println("signalState", signalState)
	})

	pubReceiver.OnConnectionStateChange(func(conState webrtc.PeerConnectionState) {
		fmt.Println("connection state", conState)
		if conState == webrtc.PeerConnectionStateClosed ||
			conState == webrtc.PeerConnectionStateDisconnected ||
			conState == webrtc.PeerConnectionStateFailed {
			// if conState == webrtc.PeerConnectionStateDisconnected {
			cancel()
		}
	})

	// Set the remote SessionDescription
	checkError(pubReceiver.SetRemoteDescription(
		webrtc.SessionDescription{
			SDP:  sdp,
			Type: webrtc.SDPTypeOffer,
		}))

	// Create answer
	answer, err := pubReceiver.CreateAnswer(nil)
	checkError(err)

	// Sets the LocalDescription, and starts our UDP listeners
	checkError(pubReceiver.SetLocalDescription(answer))

	rms.Lock()
	rms.items[user] = &rm
	rms.Unlock()

	go func() {
		<-_ctx.Done()

		checkError(pubReceiver.Close())
		rms.Lock()
		delete(rms.items, user)
		rms.Unlock()
	}()

	return answer.SDP, nil
}
