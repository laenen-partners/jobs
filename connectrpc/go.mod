module github.com/laenen-partners/jobs/connectrpc

go 1.26

require (
	connectrpc.com/connect v1.19.1
	github.com/laenen-partners/jobs v0.1.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/laenen-partners/identity v0.1.0 // indirect
	github.com/laenen-partners/pubsub v0.3.1 // indirect
)

replace github.com/laenen-partners/jobs => ../
