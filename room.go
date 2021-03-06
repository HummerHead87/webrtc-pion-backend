package chat_server

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v2"
	"github.com/segmentio/ksuid"
	"github.com/vektah/gqlparser/gqlerror"
)

type room struct {
	sync.RWMutex
	pubReceiver *webrtc.PeerConnection

	// Local track
	videoTrack *webrtc.Track
	audioTrack *webrtc.Track

	watcherJoinedChannels       map[string]chan string
	watcherDisconnectedChannels map[string]chan string
	chatMessageChannels         map[string]chan *Message

	watchers []string
}

type rooms struct {
	sync.Mutex
	items map[string]*room
}

type roomAddedChannels struct {
	sync.Mutex
	items map[string]chan string
}

type roomDeletedChannels struct {
	sync.Mutex
	items map[string]chan string
}

const (
	rtcpPLIInterval = time.Second * 3
)

var (
	rmAddedChannels   = roomAddedChannels{items: map[string]chan string{}}
	rmDeletedChannels = roomDeletedChannels{items: map[string]chan string{}}

	// Peer config
	peerConnectionConfig = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
				// URLs: []string{"stun:35.184.155.87:3478"},
			},
		},
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	rms = rooms{
		items: map[string]*room{},
	}
)

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

	rm := room{
		watchers:                    []string{},
		watcherJoinedChannels:       map[string]chan string{},
		watcherDisconnectedChannels: map[string]chan string{},
		chatMessageChannels:         map[string]chan *Message{},
	}
	_ctx, cancel := context.WithCancel(context.Background())

	// cancelled := func() bool {
	// 	select {
	// 	case <-_ctx.Done():
	// 		return true
	// 	default:
	// 		return false
	// 	}
	// }

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
			rm.Lock()
			rm.videoTrack = videoTrack
			rm.Unlock()

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
				if err == io.EOF {
					break
				}
				checkError(err)
				rm.RLock()
				_, err = rm.videoTrack.Write(rtpBuf[:i])
				rm.RUnlock()

				if err != io.ErrClosedPipe {
					checkError(err)
				}
			}
		} else {
			// Create a local audio track, all our SFU clients will be fed via this track
			audioTrack, err := pubReceiver.NewTrack(remoteTrack.PayloadType(), remoteTrack.SSRC(), "audio", "pion")
			checkError(err)
			rm.Lock()
			rm.audioTrack = audioTrack
			rm.Unlock()

			rtpBuf := make([]byte, 1400)
			for {
				i, err := remoteTrack.Read(rtpBuf)
				if err == io.EOF {
					break
				}
				checkError(err)
				rm.RLock()
				_, err = rm.audioTrack.Write(rtpBuf[:i])
				rm.RUnlock()
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

	rmAddedChannels.Lock()
	for _, ch := range rmAddedChannels.items {
		ch <- user
	}
	rmAddedChannels.Unlock()

	go func() {
		<-_ctx.Done()

		checkError(pubReceiver.Close())
		rms.Lock()
		delete(rms.items, user)
		rms.Unlock()

		rmDeletedChannels.Lock()
		for _, ch := range rmDeletedChannels.items {
			ch <- user
		}
		rmDeletedChannels.Unlock()
	}()

	return answer.SDP, nil
}

func (r *queryResolver) Rooms(ctx context.Context) ([]string, error) {
	rms.Lock()
	rooms := make([]string, 0, len(rms.items))
	for k := range rms.items {
		rooms = append(rooms, k)
	}
	rms.Unlock()

	return rooms, nil
}

func (r *queryResolver) Watchers(ctx context.Context, room string) ([]string, error) {
	rms.Lock()
	defer rms.Unlock()
	rm, ok := rms.items[room]
	if !ok {
		return nil, gqlerror.Errorf("no room with name '%v'", room)
	}

	return rm.watchers, nil
}

func (r *mutationResolver) WatchStream(ctx context.Context, stream string, user string, sdp string) (string, error) {
	rms.Lock()
	rm, ok := rms.items[stream]
	rms.Unlock()
	if !ok {
		return "", gqlerror.Errorf("stream with name '%s' doesn't exist", stream)
	}

	// Create a new PeerConnection
	subSender, err := r.api.NewPeerConnection(peerConnectionConfig)
	checkError(err)

	// Waiting for publisher track finish
	for {
		rm.RLock()
		if rm.videoTrack == nil {
			rm.RUnlock()
			//if videoTrack == nil, waiting..
			time.Sleep(100 * time.Millisecond)
		} else {
			rm.RUnlock()
			break
		}
	}

	// Add local video track
	rm.RLock()
	_, err = subSender.AddTrack(rm.videoTrack)
	rm.RUnlock()
	checkError(err)

	// Add local audio track
	rm.RLock()
	_, err = subSender.AddTrack(rm.audioTrack)
	rm.RUnlock()
	checkError(err)

	// Set the remote SessionDescription
	checkError(subSender.SetRemoteDescription(
		webrtc.SessionDescription{
			SDP:  string(sdp),
			Type: webrtc.SDPTypeOffer,
		}))

	// Create answer
	answer, err := subSender.CreateAnswer(nil)
	checkError(err)

	// Sets the LocalDescription, and starts our UDP listeners
	checkError(subSender.SetLocalDescription(answer))

	subSender.OnSignalingStateChange(func(signalState webrtc.SignalingState) {
		fmt.Printf("user %s signalingState %v\n", user, signalState)
		// fmt.Println("user signalState", signalState)
	})

	subSender.OnConnectionStateChange(func(conState webrtc.PeerConnectionState) {
		fmt.Printf("user %s conState %v\n", user, conState)
		if conState == webrtc.PeerConnectionStateDisconnected {
			// Удаляем пользователя из слайса watchers
			rm.Lock()
			var index int
			for i, item := range rm.watchers {
				if item == user {
					index = i
					break
				}
			}
			rm.watchers = append(rm.watchers[:index], rm.watchers[index+1:]...)

			for _, ch := range rm.watcherDisconnectedChannels {
				ch <- user
			}
			rm.Unlock()
		}
	})

	rm.Lock()
	rm.watchers = append(rm.watchers, user)
	for _, ch := range rm.watcherJoinedChannels {
		ch <- user
	}
	rm.Unlock()

	return answer.SDP, nil
}

func (r *subscriptionResolver) RoomAdded(ctx context.Context) (<-chan string, error) {
	ch := make(chan string, 1)
	id := ksuid.New().String()

	rmAddedChannels.Lock()
	rmAddedChannels.items[id] = ch
	rmAddedChannels.Unlock()

	// Delete channel when done
	go func() {
		<-ctx.Done()
		rmAddedChannels.Lock()
		delete(rmAddedChannels.items, id)
		rmAddedChannels.Unlock()
	}()

	return ch, nil
}

func (r *subscriptionResolver) RoomDeleted(ctx context.Context) (<-chan string, error) {
	ch := make(chan string, 1)
	id := ksuid.New().String()

	rmDeletedChannels.Lock()
	rmDeletedChannels.items[id] = ch
	rmDeletedChannels.Unlock()

	// Delete channel when done
	go func() {
		<-ctx.Done()
		rmDeletedChannels.Lock()
		delete(rmDeletedChannels.items, id)
		rmDeletedChannels.Unlock()
	}()

	return ch, nil
}

func (r *subscriptionResolver) WatcherJoined(ctx context.Context, roomName string) (<-chan string, error) {
	rms.Lock()
	rm, ok := rms.items[roomName]
	rms.Unlock()
	if !ok {
		return nil, gqlerror.Errorf("room with name '%v' doesn't exists", roomName)
	}

	ch := make(chan string, 1)
	id := ksuid.New().String()

	rm.Lock()
	rm.watcherJoinedChannels[id] = ch
	rm.Unlock()

	go func() {
		<-ctx.Done()
		rm.Lock()
		delete(rm.watcherJoinedChannels, id)
		rm.Unlock()
	}()

	return ch, nil
}

func (r *subscriptionResolver) WatcherDisconnected(ctx context.Context, roomName string) (<-chan string, error) {
	rms.Lock()
	rm, ok := rms.items[roomName]
	rms.Unlock()
	if !ok {
		return nil, gqlerror.Errorf("room with name '%v' doesn't exists", roomName)
	}

	ch := make(chan string, 1)
	id := ksuid.New().String()

	rm.Lock()
	rm.watcherDisconnectedChannels[id] = ch
	rm.Unlock()

	go func() {
		<-ctx.Done()
		rm.Lock()
		delete(rm.watcherDisconnectedChannels, id)
		rm.Unlock()
	}()

	return ch, nil
}
