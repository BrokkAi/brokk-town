# syntax=docker/dockerfile:1
#
# One Dockerfile builds Town (target bt) and each bot (targets bug-bot,
# feature-bot, issue-bot, review-bot, release-bot, simplifier-bot, repo-bot,
# mayor-bot). Base images are pinned by digest and downloaded tools by
# checksum, so a rebuild of the same commit installs the same software.
#
#   docker build --target review-bot --build-arg VERSION=v1.2.3 -t review-bot .

ARG GO_IMAGE=golang:1.27.3-trixie@sha256:c65ecfe613b539ac74c62a4c3b2c27e8f8a6076384c8f27e8a3c0f3be17cc385
ARG NODE_IMAGE=node:24-trixie-slim@sha256:54f84b4cf8432b21aaffd5d3ce8d41e12cea0d10d33c09d2f3c9879d49d6b446
ARG UV_IMAGE=ghcr.io/astral-sh/uv:0.13.6@sha256:af22a52f5eafef3d78e551599d405c02df61919d18028f0cea8eda93bc9986d0

FROM ${UV_IMAGE} AS uv

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -trimpath and a fixed build ID keep the binaries independent of where and
# when they were built.
RUN set -eux; \
    for cmd in bt bug-bot feature-bot issue-bot review-bot release-bot simplifier-bot repo-bot mayor-bot; do \
      CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -buildvcs=false \
        -ldflags "-s -w -buildid= -X main.version=${VERSION}" -o "/out/$cmd" "./cmd/$cmd"; \
    done

# runtime carries what every bot and Town shell out to: git, gh, and node/npx
# and uv/uvx for agent harnesses. Nothing uses a gh login: gh and git read
# GH_TOKEN from the environment.
FROM ${NODE_IMAGE} AS runtime
ARG TARGETARCH
ARG GH_VERSION=2.103.3
ARG GH_SHA256_AMD64=4c806978ffce1cec70853302a56d70ac6da4f19bae7adca4e1a8ab00633dd91d
ARG GH_SHA256_ARM64=ee32df2323c8847f5fac6cb6b4b69ca75828e9329a8f22f677021f7b3306ce63
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl git openssh-client tini; \
    rm -rf /var/lib/apt/lists/*; \
    case "$TARGETARCH" in \
      amd64) sum="$GH_SHA256_AMD64" ;; \
      arm64) sum="$GH_SHA256_ARM64" ;; \
      *) echo "unsupported architecture $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    echo "$sum  /tmp/gh.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/gh.tar.gz -C /tmp; \
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh" /usr/local/bin/gh; \
    rm -rf /tmp/gh*; \
    git config --system credential.https://github.com.helper '!gh auth git-credential'; \
    useradd --uid 10001 --user-group --home-dir /data --no-create-home brokk; \
    mkdir -p /data; chown brokk:brokk /data
COPY --from=uv /uv /uvx /usr/local/bin/
# The images are how Town is distributed, so they carry its license terms and
# the third-party notices of everything built into it.
COPY LICENSE NOTICE licenses/THIRD_PARTY_NOTICES.txt /usr/share/licenses/brokk-town/
# HOME is on the data volume, so npm, uv and gh caches persist with it and the
# rest of the filesystem can stay read-only.
ENV HOME=/data/home \
    GH_PROMPT_DISABLED=1 \
    GIT_TERMINAL_PROMPT=0 \
    NPM_CONFIG_UPDATE_NOTIFIER=false
USER 10001:10001
WORKDIR /data
VOLUME ["/data"]

FROM runtime AS bot
ENV BROKK_BOT_ROOT=/data \
    BROKK_BOT_LISTEN=:8080
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s CMD ["curl", "-fsS", "http://127.0.0.1:8080/v1/healthz"]

FROM bot AS bug-bot
COPY --from=build /out/bug-bot /usr/local/bin/bug-bot
ENTRYPOINT ["tini", "--", "bug-bot"]

FROM bot AS feature-bot
COPY --from=build /out/feature-bot /usr/local/bin/feature-bot
ENTRYPOINT ["tini", "--", "feature-bot"]

FROM bot AS issue-bot
COPY --from=build /out/issue-bot /usr/local/bin/issue-bot
ENTRYPOINT ["tini", "--", "issue-bot"]

FROM bot AS review-bot
COPY --from=build /out/review-bot /usr/local/bin/review-bot
ENTRYPOINT ["tini", "--", "review-bot"]

FROM bot AS release-bot
COPY --from=build /out/release-bot /usr/local/bin/release-bot
ENTRYPOINT ["tini", "--", "release-bot"]

FROM bot AS simplifier-bot
COPY --from=build /out/simplifier-bot /usr/local/bin/simplifier-bot
ENTRYPOINT ["tini", "--", "simplifier-bot"]

FROM bot AS repo-bot
COPY --from=build /out/repo-bot /usr/local/bin/repo-bot
ENTRYPOINT ["tini", "--", "repo-bot"]

FROM bot AS mayor-bot
COPY --from=build /out/mayor-bot /usr/local/bin/mayor-bot
ENTRYPOINT ["tini", "--", "mayor-bot"]

# Town serves the browser on 8080 and the managed-review callback on 8091.
# It reaches every bot at the URL its configuration names, so its image holds
# no bot binary. BROKK_TOWN_STATE_DIR lets bt status and bt web, run with
# kubectl exec, find the running service without flags.
FROM runtime AS bt
COPY --from=build /out/bt /usr/local/bin/bt
ENV BROKK_TOWN_STATE_DIR=/data
EXPOSE 8080 8091
ENTRYPOINT ["tini", "--", "bt", "--listen", "0.0.0.0:8080"]
