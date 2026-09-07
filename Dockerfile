# Titan as a contained service.
#
# The point of this image is not packaging convenience. Titan's file tools —
# read, write, edit, grep — call os.ReadFile and os.Rename directly in the host
# process; only bash has a sandbox hook (internal/tools/bash.go). So the
# in-process path check in tools.Session.Resolve is the ONLY thing standing
# between the agent and the operator's disk, and a Go-level check is not a
# boundary against an attacker who reaches the process.
#
# Running the whole process in a container moves that boundary into the kernel:
# os.ReadFile cannot name a path that does not exist in the namespace. On macOS
# the container additionally sits inside a hardware-virtualised Linux VM, so the
# host filesystem is not merely permission-denied, it is unaddressable.
#
# The Roots check remains, and is now defence in depth rather than the only wall.

# ---------------------------------------------------------------- build stage
FROM golang:1.26-bookworm AS build

WORKDIR /src

# Dependencies resolve in their own layer so a source edit does not re-download
# the module cache on every rebuild.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off produces a static binary with no libc dependency, which is what lets
# the runtime stage be as small as it is. Symbols and DWARF are stripped: they
# are debugging weight, and on an internet-facing binary they are also free
# information for anyone who obtains it.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags='-s -w' \
      -o /out/titan ./cmd/titan

# -------------------------------------------------------------- runtime stage
# Debian slim rather than distroless or scratch: the agent's whole purpose is to
# run shell commands, so it needs a shell and the ordinary POSIX tools. An image
# without them would be smaller and useless.
FROM debian:bookworm-slim

# ca-certificates is required to verify TLS to the model endpoint. git and the
# rest are what an agent working in a repository actually reaches for.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      git \
      ripgrep \
      curl \
 && rm -rf /var/lib/apt/lists/*

# A fixed non-root UID. Nothing in the image is owned by it, so even a full
# compromise of the process cannot modify the image's own contents — combined
# with a read-only rootfs at run time, the only writable surface is the
# workspace volume and the tmpfs.
RUN groupadd --gid 10001 titan \
 && useradd --uid 10001 --gid 10001 --create-home --shell /bin/bash titan

COPY --from=build /out/titan /usr/local/bin/titan

# The workspace is a mounted volume, not a path baked into the image: the whole
# design depends on no host directory being visible here.
RUN mkdir -p /workspace /home/titan/.titan \
 && chown -R titan:titan /workspace /home/titan

USER titan
WORKDIR /workspace

ENV TITAN_IN_CONTAINER=1

EXPOSE 8080

# A failing container should be restarted by the supervisor, not left serving
# errors. /v1/health is public by design so this needs no credential.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/v1/health || exit 1

ENTRYPOINT ["titan"]
CMD ["serve", "-addr", "0.0.0.0:8080"]
