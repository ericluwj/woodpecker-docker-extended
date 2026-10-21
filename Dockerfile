# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X main.buildVersion=$VERSION -X main.buildCommit=$COMMIT" \
    -o /out/woodpecker-agent-docker-extended ./cmd/woodpecker-agent-docker-extended

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /out/woodpecker-agent-docker-extended /bin/woodpecker-agent-docker-extended
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/woodpecker-docker-extended/
COPY licenses/ /usr/share/licenses/woodpecker-docker-extended/dependencies/
LABEL org.opencontainers.image.source="https://github.com/ericluwj/woodpecker-docker-extended" \
      org.opencontainers.image.licenses="MIT"
ENV WOODPECKER_BACKEND=docker-extended
ENTRYPOINT ["/bin/woodpecker-agent-docker-extended"]
