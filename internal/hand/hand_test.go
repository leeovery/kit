package hand

import "testing"

func TestAnswerReachesTheCardUp(t *testing.T) {
	d := New()
	if d.Answer("1password", Done) {
		t.Fatal("an answer with no card up was taken")
	}
	ch, down := d.Listen("1password")
	if !d.Answer("1password", Done) {
		t.Fatal("the card up didn't take its answer")
	}
	if got := <-ch; got != Done {
		t.Fatalf("got %v, want Done", got)
	}
	down()
	if d.Answer("1password", Done) {
		t.Fatal("an answer was taken after the card came down")
	}
}

func TestAnswerWaitingIsntDoubled(t *testing.T) {
	d := New()
	_, down := d.Listen("paper")
	defer down()
	if !d.Answer("paper", Done) || d.Answer("paper", Done) {
		t.Fatal("want the first answer taken and the second, while it waits, not")
	}
}

// A Stop is taken while another answer waits unread, in its place.
func TestAStopIsNeverRefused(t *testing.T) {
	d := New()
	ch, down := d.Listen("paper")
	defer down()
	if !d.Answer("paper", "r") || !d.Answer("paper", Stop) {
		t.Fatal("want the key taken, then the Stop, while the key waits")
	}
	if got := <-ch; got != Stop {
		t.Fatalf("got %v, want the Stop in the key's place", got)
	}
}

// A Stop given while a Done waits unread queues behind it: the Done's taken
// first, then the Stop, and a Stop given again with both waiting is taken,
// once.
func TestAStopQueuesBehindADone(t *testing.T) {
	d := New()
	ch, down := d.Listen("paper")
	defer down()
	if !d.Answer("paper", Done) || !d.Answer("paper", Stop) || !d.Answer("paper", Stop) {
		t.Fatal("want the Done taken, then each Stop")
	}
	var got []Answer
	for len(ch) > 0 {
		got = append(got, <-ch)
	}
	if len(got) != 2 || got[0] != Done || got[1] != Stop {
		t.Fatalf("got %v, want the Done, then the Stop", got)
	}
}

// A Done already taken leaves a Stop to wait alone.
func TestAStopAfterADoneTaken(t *testing.T) {
	d := New()
	ch, down := d.Listen("paper")
	defer down()
	d.Answer("paper", Done)
	<-ch
	if !d.Answer("paper", Stop) || <-ch != Stop || len(ch) != 0 {
		t.Fatal("want the Stop alone")
	}
}
