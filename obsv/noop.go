package obsv

func scopeName(info ServiceInfo) string {
	if info.Name != "" {
		return info.Name
	}

	return "github.com/inovacc/mantle"
}

// noopStack returns a Stack that does nothing: nil providers (accessors return
// OTel no-op tracer/meter), nil LogSink, and a no-op Shutdown. No globals set.
func noopStack(info ServiceInfo) *Stack {
	return &Stack{scope: scopeName(info)}
}
