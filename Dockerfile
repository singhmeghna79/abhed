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
      python3 \
      python3-pip \
      zip \
      unzip \
      graphviz \
 && rm -rf /var/lib/apt/lists/*

# Document generation.
#
# Titan can READ pdf/docx/xlsx/pptx — internal/tools/document.go parses them in
# pure Go, deliberately, so an air-gapped bundle needs no external binary. There
# is no writer, though: the write tool produces bytes, and a .docx is a zip of
# XML parts, so asking the model to emit one directly cannot work.
#
# These four libraries are what close that gap, and they are chosen to keep the
# property the Go parser was protecting: all are pure Python with no system
# dependencies, so the image needs no compiler, no LaTeX, and no LibreOffice —
# which would have added roughly a gigabyte for a headless office suite.
#
# --break-system-packages is correct here rather than lazy: PEP 668 guards
# against breaking a distro's own Python tooling, and this container has no
# distro tooling to break. A venv would add a layer and a PATH to maintain for
# no benefit in a single-purpose image.
RUN pip3 install --no-cache-dir --break-system-packages \
      python-docx==1.1.2 \
      openpyxl==3.1.5 \
      python-pptx==1.0.2 \
      reportlab==4.2.5 \
      pypdf==5.1.0 \
      matplotlib==3.9.2 \
      graphviz==0.20.3 \
 && python3 -c "import docx, openpyxl, pptx, reportlab, pypdf, matplotlib, graphviz; \
      print('document writers ready')"

# pypdf is here for READING, not writing, and it earns its place.
#
# The Go extractor (internal/tools/document.go) pulls literal strings out of a
# PDF's content streams, which works for a PDF whose text is stored as text. It
# fails on the common modern case: an embedded SUBSET font with a custom
# encoding, where the content stream holds glyph indices and the mapping back to
# characters lives in a ToUnicode CMap. Word exports, LaTeX, and most resume
# builders all produce those, so "read this PDF" failed on a real resume while
# succeeding on simpler files — the worst kind of bug, because it looks like it
# works until it silently does not.
#
# Implementing CMap parsing in Go is the right long-term fix. pypdf already does
# it correctly, so the extractor falls back to it rather than shipping a
# half-correct parser.

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
