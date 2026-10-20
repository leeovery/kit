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
