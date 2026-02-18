FROM public.ecr.aws/docker/library/node:20-alpine AS preact-build
WORKDIR /src
COPY preactvillacarmen/frontend/package.json preactvillacarmen/frontend/package-lock.json ./preactvillacarmen/frontend/
RUN cd preactvillacarmen/frontend && npm ci --silent
COPY preactvillacarmen/frontend ./preactvillacarmen/frontend
RUN cd preactvillacarmen/frontend && npm run build

FROM public.ecr.aws/docker/library/golang:1 AS backend-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend .
RUN mkdir -p /out
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/server ./cmd/server

FROM public.ecr.aws/docker/library/alpine:3.20
RUN apk add --no-cache ca-certificates
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=backend-build /out/server /app/server
COPY --from=preact-build /src/preactvillacarmen/frontend/dist /app/preact-dist
USER app
EXPOSE 8080
ENV STATIC_DIR=/app/preact-dist
CMD ["./server"]
