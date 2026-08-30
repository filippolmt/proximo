package docker

import (
	"context"
	"strconv"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Host port ownership. Which container publishes a host port is not a routing
// question, so it lives beside routes rather than inside them.

// PortOwner is the container publishing a host port, and whether it is one of
// proximo's own — which is the difference between a healthy machine and a
// contested port.
type PortOwner struct {
	Container string
	Stack     bool
}

// PublishedPorts maps each published host port, keyed by PortKey, to the
// container publishing it. A Check asks who holds a port rather than whether it
// is free: a healthy machine has :443 bound, by proximo.
//
// Docker is asked rather than inferred from a failed bind, because a bind is
// not a reliable answer everywhere: on BSD (macOS) SO_REUSEADDR lets a
// loopback-specific bind succeed while another process holds the wildcard
// address, which is exactly how Docker publishes :80 and :443.
func PublishedPorts(ctx context.Context) (map[string]PortOwner, error) {
	cli, err := newClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	res, err := cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, err
	}
	return portOwners(res.Items), nil
}

// PortKey is the map key PublishedPorts uses, e.g. "443/tcp".
func PortKey(port int, proto string) string {
	return strconv.Itoa(port) + "/" + proto
}

func portOwners(cs []container.Summary) map[string]PortOwner {
	owners := make(map[string]PortOwner)
	for _, c := range cs {
		_, isStack := c.Labels[roleLabel]
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			// Two containers cannot publish one host port at the same time —
			// Docker refuses the second — so there is nothing to arbitrate here.
			owners[PortKey(int(p.PublicPort), p.Type)] = PortOwner{Container: primaryName(c), Stack: isStack}
		}
	}
	return owners
}
