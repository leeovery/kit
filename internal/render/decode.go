package render

import (
	"math"
	"math/rand/v2"

	"github.com/leeovery/kit/internal/look"
)

// noiseGlyphs are what the bare machine's noise is made of.
var noiseGlyphs = []rune(`░▒▓#%&@*+=-:.01/\|<>[]{}~^`)

func smoothstep(a, b, x float64) float64 {
	t := math.Max(0, math.Min(1, (x-a)/(b-a)))
	return t * t * (3 - 2*t)
}

// wordmarkAt is where the decode's wordmark has its top left, in a window
// width by height.
func wordmarkAt(width, height int) (int, int) {
	return (width - bigWidth) / 2, (height-bigHeight)/2 - 1
}

// decode is the bare machine's noise at frame, p of the way to resolving:
// the noise thinning, the wordmark's cells locking in, a hot flash as each
// does, a band of rows glitching sideways now and then. All amber, as the
// splash has it; or coloured, as the arrival has it, the noise turning from
// amber into kit's colours, the wordmark locking in kit's gradient with a
// white flash.
func decode(width, height, frame int, p float64, coloured bool) hues {
	ox, oy := wordmarkAt(width, height)
	locks := rand.New(rand.NewPCG(7, 7))
	seed, colourful := uint64(9), 0.0
	if coloured {
		seed, colourful = 11, smoothstep(0.08, 0.45, p)
	}
	flicker := rand.New(rand.NewPCG(uint64(frame), seed))
	density := 0.5 * (1 - smoothstep(0.2, 0.85, p))
	noise := func(glyph rune, strengths ...look.Strength) hue {
		if coloured && flicker.Float64() < colourful {
			return hue{r: glyph, c: look.Colours[flicker.IntN(len(look.Colours))]}
		}
		return hue{r: glyph, s: strengths[flicker.IntN(len(strengths))]}
	}
	locked, flashing := func(int) hue { return hue{r: '█', s: look.Bright} }, hue{r: '█', s: look.Glowing}
	if coloured {
		locked = func(mx int) hue { return hue{r: '█', c: look.Gradient(float64(mx) / float64(bigWidth-1))} }
		flashing = hue{r: '█', c: flash}
	}
	g := newHues(width, height)
	for y := range height {
		for x := range width {
			lock := 0.22 + 0.55*locks.Float64()
			mx, my := x-ox, y-oy
			in := my >= 0 && my < bigHeight && mx >= 0 && mx < bigWidth && look.WordmarkPixel(mx/4, my/2)
			glyph := noiseGlyphs[flicker.IntN(len(noiseGlyphs))]
			switch {
			case in && p >= lock+0.05:
				g.set(x, y, locked(mx))
			case in && p >= lock:
				g.set(x, y, flashing)
			case in:
				if flicker.Float64() < 0.75 {
					g.set(x, y, noise(glyph, look.Bright, look.Glowing))
				}
			case flicker.Float64() < density:
				g.set(x, y, noise(glyph, look.Dimmed, look.Dimmed, look.Dimmed, look.Middle))
			}
		}
	}
	if p > 0.3 && p < 0.8 && flicker.Float64() < 0.35 {
		g.glitch(flicker.IntN(height), 1+flicker.IntN(3), flicker.IntN(17)-8)
	}
	return g
}
