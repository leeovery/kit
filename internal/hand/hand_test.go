package hand

import (
	"slices"
	"testing"
)

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

// A Skip is never refused either: it queues behind a Done waiting, takes
// the place of a key waiting, as what the person said last, and gives way
// to a Stop, given before it or after, which goes further.
func TestASkipIsNeverRefused(t *testing.T) {
	for _, tt := range []struct {
		name  string
		given []Answer
		want  []Answer
	}{
		{"alone", []Answer{Skip}, []Answer{Skip}},
		{"after a key", []Answer{"r", Skip}, []Answer{Skip}},
		{"after a Done", []Answer{Done, Skip}, []Answer{Done, Skip}},
		{"after a Stop", []Answer{Stop, Skip}, []Answer{Stop}},
		{"then a Stop", []Answer{Done, Skip, Stop}, []Answer{Done, Stop}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := New()
			ch, down := d.Listen("paper")
			defer down()
			for _, a := range tt.given {
				if !d.Answer("paper", a) {
					t.Fatalf("%v was refused", a)
				}
			}
			var got []Answer
			for len(ch) > 0 {
				got = append(got, <-ch)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
