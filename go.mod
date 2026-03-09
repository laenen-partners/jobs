module github.com/laenen-partners/jobs

go 1.25.6

require (
	connectrpc.com/connect v1.19.1
	github.com/laenen-partners/entitystore v0.0.0
)

require google.golang.org/protobuf v1.36.11

replace github.com/laenen-partners/entitystore => ../entitystore
