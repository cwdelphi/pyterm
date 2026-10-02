package server

import (
	"testing"
	"time"
)

// H4 回归：SendCh 满时 conn_type 不再一丢了之 —— 有界重试，写泵腾位后投递成功。
func TestSendConnTypeRetriesUntilQueueDrains(t *testing.T) {
	s := &Session{
		ID:     "t-h4",
		RoomID: "room-h4",
		SendCh: make(chan []byte, 1),
		Done:   make(chan struct{}),
	}
	s.SendCh <- []byte{0x01} // 占满队列，模拟背压

	go func() {
		time.Sleep(120 * time.Millisecond) // 写泵在 50ms 重试窗口内消费
		<-s.SendCh
	}()

	start := time.Now()
	if !sendConnType(s, "room-h4", "P2P", nil) {
		t.Fatalf("sendConnType: queue drained within %s, want delivered instead of dropped", time.Since(start))
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("sendConnType: expected to wait for drain, returned in %s", elapsed)
	}
}

// H4 回归：会话已关（写泵停止消费）时立即返回 false，绝不长时间挂住上报 goroutine。
func TestSendConnTypeClosedSessionReturnsQuickly(t *testing.T) {
	s := &Session{
		ID:     "t-h4b",
		RoomID: "room-h4b",
		SendCh: make(chan []byte, 1),
		Done:   make(chan struct{}),
	}
	s.SendCh <- []byte{0x01}
	close(s.Done)

	start := time.Now()
	if sendConnType(s, "room-h4b", "P2P", nil) {
		t.Fatal("sendConnType: full queue + closed session must not report success")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("sendConnType: closed session must return immediately, took %s", elapsed)
	}
}
