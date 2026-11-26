package local

// SetBeforeMergePush sets the lease-window hook of f (Task 10c
// agentDefault 9). Tests call it before Merge, never concurrently with it.
func SetBeforeMergePush(f *Forge, fn func()) { f.cfg.beforeMergePush = fn }
