package lifecycle

import (
	"cmp"
	"fmt"
	"time"
)

// spinFrames are the spinner's frames, one per SpinEvery.
var spinFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// busy announces a long step that prints nothing of its own (a unit start
// that waits for health checks, a restart): it prints <doing> once. On a
// terminal it then redraws a short "<frame> <elapsed>" line (narrow enough
// never to wrap) until the returned func is called with the step's
// outcome, which replaces it with "<done> (<elapsed>)" or, on an error,
// "<doing> failed (<elapsed>)". Without a terminal the returned func prints
// nothing. Nothing else may write to Out between the two.
func (l *L) busy(doing, done string) func(error) {
	fmt.Fprintln(l.Out, doing)
	if !l.TTY {
		return func(error) {}
	}
	start := time.Now()
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(cmp.Or(l.SpinEvery, 100*time.Millisecond))
		defer tick.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(l.Out, "\r%c %s\x1b[K", spinFrames[i%len(spinFrames)], elapsed(start))
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	return func(err error) {
		close(stop)
		<-stopped
		if err != nil {
			fmt.Fprintf(l.Out, "\r\x1b[K%s failed (%s)\n", doing, elapsed(start))
			return
		}
		fmt.Fprintf(l.Out, "\r\x1b[K%s (%s)\n", done, elapsed(start))
	}
}

// elapsed is the time since start in whole seconds: 7s, 1m02s.
func elapsed(start time.Time) string {
	d := time.Since(start).Truncate(time.Second)
	if d < time.Minute {
		return d.String()
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
