# The game and its setup checker, for running without a local Go install.
# See compose.yaml at the repository root.
FROM golang:1.26.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/enterprise ./cmd/enterprise \
 && CGO_ENABLED=0 go build -o /out/doctor ./cmd/doctor

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["enterprise"]
