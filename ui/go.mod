module github.com/laenen-partners/jobs/ui

go 1.26

replace (
	github.com/laenen-partners/jobs => ../
	github.com/laenen-partners/jobs/connectrpc => ../connectrpc
)

require (
	github.com/a-h/templ v0.3.977
	github.com/go-chi/chi/v5 v5.2.5
	github.com/jackc/pgx/v5 v5.8.0
	github.com/laenen-partners/dsx v0.5.0
	github.com/laenen-partners/identity v0.1.0
	github.com/laenen-partners/jobs v0.1.1
	github.com/laenen-partners/jobs/connectrpc v0.0.0-00010101000000-000000000000
	github.com/starfederation/datastar-go v1.1.0
)

require (
	connectrpc.com/connect v1.19.1 // indirect
	github.com/CAFxX/httpcompression v0.0.9 // indirect
	github.com/Oudwins/tailwind-merge-go v0.2.1 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.4 // indirect
	github.com/laenen-partners/migrate v0.1.0 // indirect
	github.com/laenen-partners/tags v0.1.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
