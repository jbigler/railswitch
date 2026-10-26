package lifecycle

import (
	"context"
	"fmt"
	"slices"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
)

// AttachTraefik makes the running Traefik's networks match the registry
// without restarting it (its unit-hash ignores Network= lines): it joins,
// at its fixed address, every registered project's proxy network that
// exists and it isn't on, and leaves every network it is on that is neither
// a registered project's nor the machine network. A registered network that
// doesn't exist yet is skipped: Traefik joins it when its unit restarts, or
// at the next AttachTraefik. With Traefik not running it does nothing; its
// start joins every network its unit names.
func (l *L) AttachTraefik(ctx context.Context) error {
	p := l.podman()
	running, err := p.Running(ctx, traefikContainer)
	if err != nil || !running {
		return err
	}
	attached, err := p.Networks(ctx, traefikContainer)
	if err != nil {
		return err
	}
	nets, err := p.NetworkSubnets(ctx)
	if err != nil {
		return fmt.Errorf("listing podman networks: %w", err)
	}
	reg, err := machine.Read(l.WS.Dirs.Data)
	if err != nil {
		return err
	}
	registered := []string{machine.MachineNetwork}
	for _, pe := range reg.Projects {
		net := naming.ProxyNetwork(pe.Prefix)
		registered = append(registered, net)
		if _, exists := nets[net]; !exists || slices.Contains(attached, net) {
			continue
		}
		if err := p.NetworkConnect(ctx, net, traefikContainer, machine.TraefikIP(pe.Subnet)); err != nil {
			return fmt.Errorf("attaching Traefik to %s: %w", net, err)
		}
	}
	for _, net := range attached {
		if slices.Contains(registered, net) {
			continue
		}
		if err := p.NetworkDisconnect(ctx, net, traefikContainer); err != nil {
			return fmt.Errorf("detaching Traefik from %s: %w", net, err)
		}
	}
	return nil
}
