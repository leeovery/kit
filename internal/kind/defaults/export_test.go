package defaults

// SetNever has a snapshot leave out what lists say for each macOS version,
// till the func it returns puts the lists back.
func SetNever(lists map[int]map[string][]string) func() {
	was := never
	never = lists
	return func() { never = was }
}
