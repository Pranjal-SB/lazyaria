package daemon

// Inhibitor keeps the machine from sleeping while downloads run.
// Hold is idempotent; Release is safe when nothing is held.
type Inhibitor interface {
	Hold(reason string) error
	Release() error
}
