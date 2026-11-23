package brew

// The rules of versions and tokens, for their tests.
var (
	Major        = major
	Dotted       = dotted
	Nearness     = nearness
	Replaces     = replaces
	WhyNot       = whyNot
	IsPrerelease = prerelease.MatchString
	IsLine       = lineToken
)
