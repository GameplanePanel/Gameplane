package controlplane

// A persisted payload was retrieved but failed definitive authentication or
// decoding. Consumers may preserve credentials across unknown read failures,
// but must invalidate them for this error; the marker avoids a package cycle.
type credentialStorageError struct{ error }

func (e *credentialStorageError) Unwrap() error                    { return e.error }
func (e *credentialStorageError) PermanentCredentialFailure() bool { return true }
