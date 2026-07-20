package relay

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func newPooledTestReader(t *testing.T, key wsPoolKey) (*wsUpstreamReader, *websocket.Conn) {
	t.Helper()
	clientConn, serverConn := newTestWSConnPair(t)
	pc := &pooledConn{
		id:        nextWSConnID(),
		conn:      clientConn,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		busy:      true,
		queue:     1,
		poolKey:   key,
	}
	wsUpstreamPool.mu.Lock()
	entry := wsUpstreamPool.conns[key]
	if entry == nil {
		entry = &wsPoolEntry{}
		wsUpstreamPool.conns[key] = entry
	}
	entry.conns = append(entry.conns, pc)
	wsUpstreamPool.mu.Unlock()
	return newWSUpstreamReader(pc, key.channelID, key.keyID), serverConn
}

func pooledIdleCount(key wsPoolKey) int {
	wsUpstreamPool.mu.Lock()
	defer wsUpstreamPool.mu.Unlock()
	count := 0
	if entry := wsUpstreamPool.conns[key]; entry != nil {
		for _, pc := range entry.conns {
			if pc != nil && !pc.busy {
				count++
			}
		}
	}
	return count
}

// 完整读到终态事件的干净连接应回池复用。
func TestWSReaderCloseCleanReturnsToPool(t *testing.T) {
	resetWSUpstreamPool()
	t.Cleanup(resetWSUpstreamPool)

	key := wsPoolKey{channelID: 950001, keyID: 1}
	reader, serverConn := newPooledTestReader(t, key)
	defer serverConn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := serverConn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.completed","response":{"status":"completed"}}`)); err != nil {
		t.Fatalf("server write: %v", err)
	}
	if _, err := reader.ReadEvent(ctx); err != nil {
		t.Fatalf("ReadEvent terminal: %v", err)
	}
	_ = reader.Close()

	if got := pooledIdleCount(key); got != 1 {
		t.Fatalf("clean connection should be pooled, idle=%d", got)
	}
}

// 回归：流中途出错的连接不得回池，否则下一个请求会拿到带残留事件的脏连接。
func TestWSReaderCloseAfterErrorRemovesConn(t *testing.T) {
	resetWSUpstreamPool()
	t.Cleanup(resetWSUpstreamPool)

	key := wsPoolKey{channelID: 950002, keyID: 1}
	reader, serverConn := newPooledTestReader(t, key)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 上游异常断开
	_ = serverConn.Close(websocket.StatusInternalError, "boom")
	if _, err := reader.ReadEvent(ctx); err == nil {
		t.Fatal("expected read error after abnormal close")
	}
	_ = reader.Close()

	if got := pooledIdleCount(key); got != 0 {
		t.Fatalf("dirty connection must not be pooled, idle=%d", got)
	}
}

// 回归：未读到终态就关闭（如客户端取消）的连接上可能有在途事件，不得回池。
func TestWSReaderCloseBeforeTerminalRemovesConn(t *testing.T) {
	resetWSUpstreamPool()
	t.Cleanup(resetWSUpstreamPool)

	key := wsPoolKey{channelID: 950003, keyID: 1}
	reader, serverConn := newPooledTestReader(t, key)
	defer serverConn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := serverConn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.output_text.delta","delta":"partial"}`)); err != nil {
		t.Fatalf("server write: %v", err)
	}
	if _, err := reader.ReadEvent(ctx); err != nil {
		t.Fatalf("ReadEvent delta: %v", err)
	}
	// 终态未达成时上层放弃（客户端取消路径走的就是无条件 Close）
	_ = reader.Close()

	if got := pooledIdleCount(key); got != 0 {
		t.Fatalf("incomplete connection must not be pooled, idle=%d", got)
	}
}

// 回归：preflight ping 解锁窗口内连接必须处于预占状态，
// 并发 GetPreferred 不得把同一条连接交给第二个请求（跨用户串流）。
func TestWSPreflightReservesConnDuringPing(t *testing.T) {
	resetWSUpstreamPool()
	t.Cleanup(resetWSUpstreamPool)

	clientConn, serverConn := newTestWSConnPair(t)
	defer serverConn.Close(websocket.StatusNormalClosure, "")
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	// 服务端进入读循环以便自动响应 ping
	go func() {
		for {
			if _, _, err := serverConn.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	// coder/websocket 的 Ping 需要本端有并发 Reader 才能收到 pong，起一个客户端读泵
	go func() {
		for {
			if _, _, err := clientConn.Read(context.Background()); err != nil {
				return
			}
		}
	}()

	key := wsPoolKey{channelID: 950004, keyID: 1}
	pc := &pooledConn{
		id:        nextWSConnID(),
		conn:      clientConn,
		createdAt: time.Now(),
		lastUsed:  time.Now().Add(-2 * wsHealthCheckIdle), // 触发 preflight ping
		poolKey:   key,
	}
	wsUpstreamPool.mu.Lock()
	wsUpstreamPool.conns[key] = &wsPoolEntry{conns: []*pooledConn{pc}}
	wsUpstreamPool.mu.Unlock()

	const contenders = 8
	results := make(chan *pooledConn, contenders)
	for i := 0; i < contenders; i++ {
		go func() {
			results <- wsUpstreamPool.GetPreferred(key, pc.id)
		}()
	}

	acquired := 0
	for i := 0; i < contenders; i++ {
		if got := <-results; got != nil {
			acquired++
		}
	}
	if acquired > 1 {
		t.Fatalf("preflight window handed the same conn to %d requests", acquired)
	}
	if acquired == 0 {
		t.Fatal("expected exactly one request to win the preferred conn")
	}
}
