package steps

import "time"

// SetManualPoll has a raised step by hand's command run every d, for a
// test, and returns what puts it back.
func SetManualPoll(d time.Duration) (restore func()) {
	was := manualPoll
	manualPoll = d
	return func() { manualPoll = was }
}

// A card's waits, for a test: how long one whose turn has come waits to go
// up, and how long one's up before its notification goes out.
const (
	DrawAfter   = drawAfter
	NoticeAfter = noticeAfter
)

// NoticesSent waits till the notifications r's cards have given have gone
// out, for a test.
func NoticesSent(r *Raise) {
	if q := r.queue(); q != nil {
		q.Wait()
	}
}

// UpAsEmitted is the item of the card up, "" for none, as an event r emits
// finds it, for a test: read without the lock r holds as it emits, so only
// from a sink's Emit.
func UpAsEmitted(r *Raise) string {
	if r.shown == nil {
		return ""
	}
	return r.shown.up
}
