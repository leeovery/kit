package relay

// SetOwner has r answer the processes of the user uid alone.
func SetOwner(r *Relay, uid int) { r.owner = uid }
