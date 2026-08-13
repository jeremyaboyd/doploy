package deploy

import (
	"fmt"
	"strings"

	"github.com/jeremyaboyd/doploy/internal/spec"
)

// dockerBootstrap installs Docker and the compose plugin when they are missing.
//
// This runs Docker's official convenience script from get.docker.com. That is
// the documented install path and the same thing DigitalOcean's own tutorials
// use, but it is a remote script executed as root, so `bootstrap: false` in the
// spec (or --no-bootstrap) skips it entirely for hosts you prepare yourself.
const dockerBootstrap = `
set -eu

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  echo "docker $(docker --version | awk '{print $3}' | tr -d ,) and compose already present"
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "installing docker from get.docker.com"
  export DEBIAN_FRONTEND=noninteractive
  curl -fsSL https://get.docker.com -o /tmp/doploy-get-docker.sh
  sh /tmp/doploy-get-docker.sh
  rm -f /tmp/doploy-get-docker.sh
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "installing docker compose plugin"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq docker-compose-plugin
fi

systemctl enable --now docker
docker --version
docker compose version
`

// mountScript formats, mounts, and persists a DigitalOcean block volume.
//
// The device path is the stable by-id link DigitalOcean creates for attached
// volumes. Formatting only happens when the device has no filesystem, so
// re-running a deploy never destroys data on an existing volume.
func mountScript(volumeName, mountPath, fsType string) string {
	if fsType == "" {
		fsType = "ext4"
	}
	device := "/dev/disk/by-id/scsi-0DO_Volume_" + volumeName

	return fmt.Sprintf(`
set -eu

DEVICE=%s
MOUNT=%s
FSTYPE=%s

# The by-id link appears a moment after the API reports the attach as done.
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  [ -e "$DEVICE" ] && break
  sleep 2
done

if [ ! -e "$DEVICE" ]; then
  echo "block device $DEVICE never appeared; is the volume attached?" >&2
  exit 1
fi

if ! blkid "$DEVICE" >/dev/null 2>&1; then
  echo "formatting $DEVICE as $FSTYPE"
  mkfs."$FSTYPE" -F "$DEVICE"
else
  echo "$DEVICE already has a filesystem, leaving it alone"
fi

mkdir -p "$MOUNT"

if ! grep -qs "^$DEVICE " /etc/fstab; then
  echo "$DEVICE $MOUNT $FSTYPE defaults,nofail,discard 0 0" >> /etc/fstab
fi

if ! mountpoint -q "$MOUNT"; then
  mount "$MOUNT"
fi

echo "mounted $DEVICE at $MOUNT"
`, shellQuote(device), shellQuote(mountPath), shellQuote(fsType))
}

// composeUpScript pulls images, builds any local contexts, and brings the stack
// up.
func composeUpScript(dir, project string, wait, pruneImages, hasBuilds bool) string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	fmt.Fprintf(&b, "cd %s\n", shellQuote(dir))

	// With build services present, pull would fail on images that do not exist
	// in any registry yet, so tolerate those failures and let build supply them.
	if hasBuilds {
		fmt.Fprintf(&b, "docker compose -p %s pull --ignore-pull-failures || true\n", shellQuote(project))
		fmt.Fprintf(&b, "docker compose -p %s build\n", shellQuote(project))
	} else {
		fmt.Fprintf(&b, "docker compose -p %s pull\n", shellQuote(project))
	}

	up := fmt.Sprintf("docker compose -p %s up -d --remove-orphans", shellQuote(project))
	if wait {
		// --wait blocks until healthchecks pass, turning a green deploy into a
		// real signal rather than "the containers started".
		up += " --wait"
	}
	b.WriteString(up + "\n")

	if pruneImages {
		b.WriteString("docker image prune -f >/dev/null 2>&1 || true\n")
	}
	return b.String()
}

// statusSeparator delimits fields in the compose ps output.
//
// A tab cannot be used here: the format string is a Go template passed through
// a shell, where "\t" stays two literal characters rather than becoming a tab.
const statusSeparator = "|"

// composePSScript reports what is running, for the post-deploy summary.
func composePSScript(dir, project string) string {
	format := "{{.Service}}" + statusSeparator + "{{.State}}" + statusSeparator + "{{.Status}}"
	return fmt.Sprintf("cd %s && docker compose -p %s ps --format %s",
		shellQuote(dir), shellQuote(project), shellQuote(format))
}

// registryLoginScript authenticates to a private registry.
//
// The password is fed through stdin rather than argv so it never appears in the
// droplet's process list.
func registryLoginScript(host string, reg *spec.Registry) string {
	return fmt.Sprintf("printf '%%s' %s | docker login %s -u %s --password-stdin",
		shellQuote(reg.Password), shellQuote(host), shellQuote(reg.Username))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
