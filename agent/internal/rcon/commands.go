package rcon

import "context"

// Executor is the command transport shared by all handlers for one game.
type Executor interface {
	Exec(string) (string, error)
}

// Commands admits either one command or one complete backup sequence at a time.
type Commands struct {
	transport Executor
	busy      chan struct{}
}

// NewCommands shares one admission queue across clients of a transport.
func NewCommands(transport Executor) *Commands {
	return &Commands{transport: transport, busy: make(chan struct{}, 1)}
}

// Exec admits one command when no sequence is in progress.
func (c *Commands) Exec(command string) (string, error) {
	c.busy <- struct{}{}
	defer func() { <-c.busy }()
	return c.transport.Exec(command)
}

// Sequence gives run exclusive use of the underlying transport, including
// any compensating commands. A canceled waiter must never start a late pause.
func (c *Commands) Sequence(ctx context.Context, run func(Executor) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.busy <- struct{}{}:
	}
	defer func() { <-c.busy }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return run(c.transport)
}
