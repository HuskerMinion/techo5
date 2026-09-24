# dashcast: Home Assistant dashboards streamed to TECHO5 screens. See README.md.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dashcast .

# Chrome's headless shell, and fonts for the text a dashboard can hold.
FROM chromedp/headless-shell:stable
RUN apt-get update \
 && apt-get install -y --no-install-recommends fonts-noto-core fonts-noto-color-emoji ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /dashcast /usr/local/bin/dashcast
ENV LISTEN=:9555
EXPOSE 9555
ENTRYPOINT ["/usr/local/bin/dashcast"]
