package rcon

import "context"

// Executor is the command transport shared by all handlers for one game.
type Executor interface {
	Exec(string) (string, error)
}

// WithContext binds command admission to the caller's lifetime. Plain
// transports have no admission queue and keep their own I/O deadlines.
func WithContext(ctx context.Context, rc Executor) Executor {
	if commands, ok := rc.(interface {
		ExecContext(context.Context, string) (string, error)
	}); ok {
		return commandFunc(func(command string) (string, error) {
			return commands.ExecContext(ctx, command)
		})
	}
	return rc
}

type commandFunc func(string) (string, error)

func (f commandFunc) Exec(command string) (string, error) { return f(command) }

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
	return c.ExecContext(context.Background(), command)
}

// ExecContext cancels a queued command before it reaches the transport.
func (c *Commands) ExecContext(ctx context.Context, command string) (string, error) {
	var reply string
	err := c.Sequence(ctx, func(transport Executor) error {
		var err error
		reply, err = transport.Exec(command)
		return err
	})
	return reply, err
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
