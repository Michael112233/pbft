# Regenerating Protobuf and gRPC Code

Use this guide after changing either protobuf contract:

- `proto/pbft_transport.proto`
- `proto/learningagent/learning_agent.proto`

Run every command from the repository root. Do not edit generated `.pb.go`,
`_pb2.py`, or `_pb2_grpc.py` files manually.

## Prerequisites

The Go generators must be installed and available to `protoc`:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
export PATH="$PATH:$HOME/go/bin"
```

Check the tools:

```bash
protoc --version
protoc-gen-go --version
protoc-gen-go-grpc --version
```

For Python generation, activate the repository virtual environment:

```bash
source venv/bin/activate
python --version
python -m grpc_tools.protoc --version
```

If `grpc_tools` is unavailable, install the repository requirements inside the
virtual environment:

```bash
python -m pip install -r requirements.txt
```

## PBFT transport

After changing `proto/pbft_transport.proto`, regenerate its Go message and gRPC
bindings:

```bash
PATH="$PATH:$HOME/go/bin" protoc \
  -I proto \
  --go_out=. \
  --go_opt=module=github.com/michael112233/pbft \
  --go-grpc_out=. \
  --go-grpc_opt=module=github.com/michael112233/pbft \
  proto/pbft_transport.proto
```

This updates:

- `transportpb/pbft_transport.pb.go`
- `transportpb/pbft_transport_grpc.pb.go`

The PBFT transport currently has no Python bindings in this repository.

## Learning agent

After changing `proto/learningagent/learning_agent.proto`, regenerate both the
Go and Python bindings.

Generate Go:

```bash
PATH="$PATH:$HOME/go/bin" protoc \
  -I proto \
  --go_out=. \
  --go_opt=module=github.com/michael112233/pbft \
  --go-grpc_out=. \
  --go-grpc_opt=module=github.com/michael112233/pbft \
  proto/learningagent/learning_agent.proto
```

This updates:

- `learningagentpb/learning_agent.pb.go`
- `learningagentpb/learning_agent_grpc.pb.go`

Generate Python from the activated virtual environment:

```bash
python -m grpc_tools.protoc \
  -I proto \
  --python_out=. \
  --grpc_python_out=. \
  proto/learningagent/learning_agent.proto
```

This updates:

- `learningagent/learning_agent_pb2.py`
- `learningagent/learning_agent_pb2_grpc.py`

## Verify the generated code

Review the generated changes before changing application code:

```bash
git diff -- \
  transportpb \
  learningagentpb \
  learningagent/learning_agent_pb2.py \
  learningagent/learning_agent_pb2_grpc.py
```

Then run the relevant tests:

```bash
go test ./transportpb ./learningagentpb
go test -vet=off ./node -run LearningAgent -count=1
python -m unittest learningagent.test_address learningagent.test_server
git diff --check
```

If only one contract changed, it is sufficient to run that contract's package
tests plus any packages that consume it.

## Schema compatibility rules

- Never reuse or change the meaning of an existing field number.
- When removing a field, reserve its number and preferably its old name.
- Add new fields with new numbers; old clients safely ignore unknown fields.
- Adding an RPC requires implementing and registering its handler. Code
  generation alone does not implement server behavior.
- A generated client and server can compile while still disagreeing at runtime
  if only one side was regenerated or deployed. Regenerate and test both sides.
- Protobuf maps work across Go and Python, but map iteration order must not be
  used as machine-learning feature order. Convert maps using an explicit,
  stable feature list.

Example of reserving a removed field:

```protobuf
message Example {
  reserved 3;
  reserved "old_field";

  int32 node_id = 1;
  string new_field = 4;
}
```

## Common problems

`protoc-gen-go: program not found` means `$HOME/go/bin` is missing from
`PATH`. Use the commands above or add this to your shell configuration:

```bash
export PATH="$PATH:$HOME/go/bin"
```

`No module named grpc_tools` usually means the virtual environment is not
active or its dependencies are not installed. Confirm that `which python`
points inside `pbftm/venv`.

Python errors such as `unexpected keyword argument` after regeneration mean the
application or tests still use an old protobuf field name. Update those callers
to match the new contract.

An `UNIMPLEMENTED` gRPC response means the RPC is missing from the registered
server implementation, or the process is running stale generated code.
