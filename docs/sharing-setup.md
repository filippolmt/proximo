# Setting up sharing for a team

[← back to docs index](README.md)

The steps to go from nothing to a colleague opening a page that runs on your
machine. [Sharing a route with colleagues](sharing.md) explains why each step
exists and what it guarantees. This guide is the order to do them in.

Every value is a placeholder: the Peer suffix `mesh.internal`, the machine
label `studio-01`, and `<mesh-ip>` for a machine's own address on the mesh.

## Who does what

One person can hold several roles, and a machine that shares also opens other
people's pages.

| Role | Does | How often |
| --- | --- | --- |
| **Mesh operator** | Runs the team's mesh: a group of machines, an access policy, and one DNS forwarding entry per sharing machine. | Once, then one entry per sharing machine |
| **Custodian** | Two people. Each keeps one copy of the team root key offline, and either of them signs a sharing machine's intermediate. | Once, then once per sharing machine every five years |
| **Sharing machine** | Exposes its own routes. In the glossary, a [Publishing peer](../CONTEXT.md). | Once per machine |
| **Colleague** | Opens other people's pages. In the glossary, a [Resolving peer](../CONTEXT.md). | Once per machine |

## Once per team: the team root

A custodian mints the team root once, from a checkout of this repository, with
Docker as the only prerequisite:

```sh
mkdir team-root && cd team-root
../tools/team-ca/team-ca.sh root mesh.internal      # team-root.crt + team-root.key
openssl x509 -in team-root.crt -noout -fingerprint -sha256
```

- **`team-root.key`**: encrypt it, keep two copies on offline media held by the
  two custodians, and remove the plaintext. The custody rules and why no service
  may hold the key are in
  [the team root and the intermediates](sharing.md#the-team-root-and-the-intermediates).
- **`team-root.crt`** is public. Publish it where the team finds it, together
  with its SHA-256, so each colleague can check they received the right one.

## Once per team: the mesh

The mesh operator sets up what
[the mesh must provide](sharing.md#what-the-mesh-must-provide):

1. **A group** holding the machines that take part.
2. **An access policy** from that group to itself, both directions, admitting
   TCP 443 and port 5354 over TCP and UDP, nothing else.
3. **A map of sharing machines**, `label → mesh IP`, which starts empty.
4. **One DNS forwarding entry per item in that map**: the domain
   `<label>.mesh.internal` goes to `<mesh-ip>` on port **5354**, never as the
   primary resolver, distributed to the group.

Declare the map in infrastructure-as-code. Adding a sharing machine is then one
line, and removing it is one line. With NetBird, item 4 is a nameserver group.
The fields to write explicitly, so no default substitutes port 53 or turns the
group primary, are in
[the appendix](sharing.md#appendix--one-transport-that-satisfies-the-requirements).

## Enrolling a colleague

On the colleague's machine:

1. Join the mesh and the group from [the mesh](#once-per-team-the-mesh).
2. Install proximo ([Installation](installation.md#step-1--install-the-binary)).
   Docker is not needed to open other people's pages.
3. On Linux, start Chrome once and quit it, so it creates its certificate store.
   `proximo trust` installs only into stores that exist.
4. Check the SHA-256 of `team-root.crt` against the published one, then:

   ```sh
   proximo config peer-suffix mesh.internal
   proximo config team-root ~/team-root.crt
   proximo trust
   ```

5. Fully restart the browser. Use Chrome 126 or later, or Firefox.

Run `proximo trust` as yourself, never under `sudo`: it asks for `sudo` itself.
Under `sudo`, Ubuntu sets `HOME` to root's, so proximo reads root's
configuration, finds no team root and installs nothing for you.

## Enrolling a sharing machine

Once per machine, in this order:

1. Pick the machine label. It names the machine, not a person, and stays stable:
   colleagues bookmark it ([`config machine`](cli.md#proximo-config-machine)).
2. Read the machine's mesh IP from the mesh client, then:

   ```sh
   proximo config peer-suffix mesh.internal
   proximo config machine studio-01
   proximo config address <mesh-ip>
   proximo config team-root ~/team-root.crt
   proximo config csr > machine.csr
   ```

3. Send `machine.csr` to a custodian over any channel, since it is public. The
   custodian decrypts the key into a temporary directory, signs, and removes the
   plaintext:

   ```sh
   tools/team-ca/team-ca.sh sign mesh.internal studio-01 team-root.crt team-root.key machine.csr > intermediate.crt
   ```

4. Install what comes back, then start:

   ```sh
   proximo config intermediate intermediate.crt
   proximo trust
   proximo up
   proximo doctor
   ```

5. Confirm the peer DNS service answers, from **another** machine on the mesh:

   ```sh
   dig @<mesh-ip> -p 5354 proximo-doctor.studio-01.mesh.internal +short   # <mesh-ip>
   ```

   On macOS `peer-dns` can fail on the sharing machine itself while this answers
   ([proximo does not answer on the mesh address](troubleshooting.md#proximo-does-not-answer-on-the-mesh-address)).
6. **Only then** the mesh operator adds `studio-01 → <mesh-ip>` to the map. An
   entry created earlier looks live and resolves nothing.

## Sharing a page

Add one label to the container:

```yaml
services:
  kuma:
    image: louislam/uptime-kuma:1
    labels:
      - proximo.hosts=kuma.test
      - proximo.share=true
```

`proximo status` prints its peer names in the `PEER` column
([the PEER column](cli.md#the-peer-column)). Send the **Qualified** one, here
`https://kuma.<project>.studio-01.mesh.internal`: a Collision never moves it.
Nothing changes on the mesh or on the colleague's machine for a new page,
because the whole `studio-01` subtree already reaches you.

Before sending it, open the page once on its peer name. An app that builds
links from a configured base URL, or pins a cookie to a `Domain`, breaks there
([what an app needs to be shareable](routing.md#what-an-app-needs-to-be-shareable)).
Every route you share is reachable by everyone the mesh admits to your machine.
To narrow one, add `proximo.auth`
([who can reach a shared route](sharing.md#who-can-reach-a-shared-route)).

## Checking it end to end

From a colleague's machine:

```sh
# the subtree reaches the sharing machine
resolvectl query proximo-doctor.studio-01.mesh.internal       # Linux
dscacheutil -q host -a name proximo-doctor.studio-01.mesh.internal   # macOS

# the shared name verifies against the team root
openssl s_client -connect <mesh-ip>:443 -servername <name> -verify_hostname <name> \
  -verify_return_error </dev/null 2>&1 | grep "Verify return code"   # 0 (ok)
```

On macOS use `dscacheutil`, never `dig` without `@`: `dig` bypasses the scoped
resolvers the mesh installs. An unshared or invented name receives a certificate
with no name at all. That is the expected answer, not a fault. A failure has
its own section in [troubleshooting](troubleshooting.md#a-shared-link-does-not-resolve-or-times-out).

## Stopping

| To stop | Do |
| --- | --- |
| One page | Remove `proximo.share`. |
| The whole machine, for a while | Bring the mesh client down. No colleague reaches the machine, whatever proximo does. |
| The whole machine, for good | `proximo config unset machine`, then `proximo up`. The mesh operator removes its line from the map. |
| A colleague | Remove them from the mesh. A certificate alone reaches nothing ([no revocation](sharing.md#the-team-root-and-the-intermediates)). |

With no peer value configured, proximo runs no peer path at all.
