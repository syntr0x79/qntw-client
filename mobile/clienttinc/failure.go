package clienttinc

// RuntimeError identifies a local setup stage without publishing the command
// output, private files or credential-bearing server error that caused it.
type RuntimeError struct{ Code string }

func (e RuntimeError) Error() string { return "client tinc runtime: " + e.Code }
