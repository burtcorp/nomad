package nomad

import (
	"fmt"
	"io"
	"log"
	"time"

	"github.com/armon/go-metrics"
	"github.com/hashicorp/go-msgpack/codec"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/raft"
)

var (
	msgpackHandle = &codec.MsgpackHandle{
		RawToString: true,
		WriteExt:    true,
	}
)

// SnapshotType is prefixed to a record in the FSM snapshot
// so that we can determine the type for restore
type SnapshotType byte

const (
	NodeSnapshot SnapshotType = iota
	JobSnapshot
	IndexSnapshot
)

// nomadFSM implements a finite state machine that is used
// along with Raft to provide strong consistency. We implement
// this outside the Server to avoid exposing this outside the package.
type nomadFSM struct {
	logOutput io.Writer
	logger    *log.Logger
	state     *StateStore
}

// nomadSnapshot is used to provide a snapshot of the current
// state in a way that can be accessed concurrently with operations
// that may modify the live state.
type nomadSnapshot struct {
	snap *StateSnapshot
}

// snapshotHeader is the first entry in our snapshot
type snapshotHeader struct {
}

// NewFSMPath is used to construct a new FSM with a blank state
func NewFSM(logOutput io.Writer) (*nomadFSM, error) {
	// Create a state store
	state, err := NewStateStore(logOutput)
	if err != nil {
		return nil, err
	}

	fsm := &nomadFSM{
		logOutput: logOutput,
		logger:    log.New(logOutput, "", log.LstdFlags),
		state:     state,
	}
	return fsm, nil
}

// Close is used to cleanup resources associated with the FSM
func (n *nomadFSM) Close() error {
	return nil
}

// State is used to return a handle to the current state
func (n *nomadFSM) State() *StateStore {
	return n.state
}

func (n *nomadFSM) Apply(log *raft.Log) interface{} {
	buf := log.Data
	msgType := structs.MessageType(buf[0])

	// Check if this message type should be ignored when unknown. This is
	// used so that new commands can be added with developer control if older
	// versions can safely ignore the command, or if they should crash.
	ignoreUnknown := false
	if msgType&structs.IgnoreUnknownTypeFlag == structs.IgnoreUnknownTypeFlag {
		msgType &= ^structs.IgnoreUnknownTypeFlag
		ignoreUnknown = true
	}

	switch msgType {
	case structs.NodeRegisterRequestType:
		return n.applyRegisterNode(buf[1:], log.Index)
	case structs.NodeDeregisterRequestType:
		return n.applyDeregisterNode(buf[1:], log.Index)
	case structs.NodeUpdateStatusRequestType:
		return n.applyStatusUpdate(buf[1:], log.Index)
	case structs.JobRegisterRequestType:
		return n.applyRegisterJob(buf[1:], log.Index)
	case structs.JobDeregisterRequestType:
		return n.applyDeregisterJob(buf[1:], log.Index)
	default:
		if ignoreUnknown {
			n.logger.Printf("[WARN] nomad.fsm: ignoring unknown message type (%d), upgrade to newer version", msgType)
			return nil
		} else {
			panic(fmt.Errorf("failed to apply request: %#v", buf))
		}
	}
}

func (n *nomadFSM) applyRegisterNode(buf []byte, index uint64) interface{} {
	defer metrics.MeasureSince([]string{"nomad", "fsm", "register_node"}, time.Now())
	var req structs.NodeRegisterRequest
	if err := structs.Decode(buf, &req); err != nil {
		panic(fmt.Errorf("failed to decode request: %v", err))
	}

	if err := n.state.RegisterNode(index, req.Node); err != nil {
		n.logger.Printf("[ERR] nomad.fsm: RegisterNode failed: %v", err)
		return err
	}
	return nil
}

func (n *nomadFSM) applyDeregisterNode(buf []byte, index uint64) interface{} {
	defer metrics.MeasureSince([]string{"nomad", "fsm", "deregister_node"}, time.Now())
	var req structs.NodeDeregisterRequest
	if err := structs.Decode(buf, &req); err != nil {
		panic(fmt.Errorf("failed to decode request: %v", err))
	}

	if err := n.state.DeregisterNode(index, req.NodeID); err != nil {
		n.logger.Printf("[ERR] nomad.fsm: DeregisterNode failed: %v", err)
		return err
	}
	return nil
}

func (n *nomadFSM) applyStatusUpdate(buf []byte, index uint64) interface{} {
	defer metrics.MeasureSince([]string{"nomad", "fsm", "node_status_update"}, time.Now())
	var req structs.NodeUpdateStatusRequest
	if err := structs.Decode(buf, &req); err != nil {
		panic(fmt.Errorf("failed to decode request: %v", err))
	}

	if err := n.state.UpdateNodeStatus(index, req.NodeID, req.Status); err != nil {
		n.logger.Printf("[ERR] nomad.fsm: UpdateNodeStatus failed: %v", err)
		return err
	}
	return nil
}

func (n *nomadFSM) applyRegisterJob(buf []byte, index uint64) interface{} {
	defer metrics.MeasureSince([]string{"nomad", "fsm", "register_job"}, time.Now())
	var req structs.JobRegisterRequest
	if err := structs.Decode(buf, &req); err != nil {
		panic(fmt.Errorf("failed to decode request: %v", err))
	}

	if err := n.state.RegisterJob(index, req.Job); err != nil {
		n.logger.Printf("[ERR] nomad.fsm: RegisterJob failed: %v", err)
		return err
	}
	return nil
}

func (n *nomadFSM) applyDeregisterJob(buf []byte, index uint64) interface{} {
	defer metrics.MeasureSince([]string{"nomad", "fsm", "deregister_job"}, time.Now())
	var req structs.JobDeregisterRequest
	if err := structs.Decode(buf, &req); err != nil {
		panic(fmt.Errorf("failed to decode request: %v", err))
	}

	if err := n.state.DeregisterJob(index, req.JobID); err != nil {
		n.logger.Printf("[ERR] nomad.fsm: DeregisterJob failed: %v", err)
		return err
	}
	return nil
}

func (n *nomadFSM) Snapshot() (raft.FSMSnapshot, error) {
	// Create a new snapshot
	snap, err := n.state.Snapshot()
	if err != nil {
		return nil, err
	}
	return &nomadSnapshot{snap}, nil
}

func (n *nomadFSM) Restore(old io.ReadCloser) error {
	defer old.Close()

	// Create a new state store
	state, err := NewStateStore(n.logOutput)
	if err != nil {
		return err
	}
	n.state = state

	// Start the state restore
	restore, err := state.Restore()
	if err != nil {
		return err
	}
	defer restore.Abort()

	// Create a decoder
	dec := codec.NewDecoder(old, msgpackHandle)

	// Read in the header
	var header snapshotHeader
	if err := dec.Decode(&header); err != nil {
		return err
	}

	// Populate the new state
	msgType := make([]byte, 1)
	for {
		// Read the message type
		_, err := old.Read(msgType)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		// Decode
		switch SnapshotType(msgType[0]) {
		case NodeSnapshot:
			node := new(structs.Node)
			if err := dec.Decode(node); err != nil {
				return err
			}
			if err := restore.NodeRestore(node); err != nil {
				return err
			}

		case JobSnapshot:
			job := new(structs.Job)
			if err := dec.Decode(job); err != nil {
				return err
			}
			if err := restore.JobRestore(job); err != nil {
				return err
			}

		case IndexSnapshot:
			idx := new(IndexEntry)
			if err := dec.Decode(idx); err != nil {
				return err
			}
			if err := restore.IndexRestore(idx); err != nil {
				return err
			}

		default:
			return fmt.Errorf("Unrecognized snapshot type: %v", msgType)
		}
	}

	// Commit the state restore
	restore.Commit()
	return nil
}

func (s *nomadSnapshot) Persist(sink raft.SnapshotSink) error {
	defer metrics.MeasureSince([]string{"nomad", "fsm", "persist"}, time.Now())
	// Register the nodes
	encoder := codec.NewEncoder(sink, msgpackHandle)

	// Write the header
	header := snapshotHeader{}
	if err := encoder.Encode(&header); err != nil {
		sink.Cancel()
		return err
	}

	// Write all the data out
	if err := s.persistIndexes(sink, encoder); err != nil {
		sink.Cancel()
		return err
	}
	if err := s.persistNodes(sink, encoder); err != nil {
		sink.Cancel()
		return err
	}
	if err := s.persistJobs(sink, encoder); err != nil {
		sink.Cancel()
		return err
	}
	return nil
}

func (s *nomadSnapshot) persistIndexes(sink raft.SnapshotSink,
	encoder *codec.Encoder) error {
	// Get all the indexes
	iter, err := s.snap.Indexes()
	if err != nil {
		return err
	}

	for {
		// Get the next item
		raw := iter.Next()
		if raw == nil {
			break
		}

		// Prepare the request struct
		idx := raw.(*IndexEntry)

		// Write out a node registration
		sink.Write([]byte{byte(IndexSnapshot)})
		if err := encoder.Encode(idx); err != nil {
			return err
		}
	}
	return nil
}

func (s *nomadSnapshot) persistNodes(sink raft.SnapshotSink,
	encoder *codec.Encoder) error {
	// Get all the nodes
	nodes, err := s.snap.Nodes()
	if err != nil {
		return err
	}

	for {
		// Get the next item
		raw := nodes.Next()
		if raw == nil {
			break
		}

		// Prepare the request struct
		node := raw.(*structs.Node)

		// Write out a node registration
		sink.Write([]byte{byte(NodeSnapshot)})
		if err := encoder.Encode(node); err != nil {
			return err
		}
	}
	return nil
}

func (s *nomadSnapshot) persistJobs(sink raft.SnapshotSink,
	encoder *codec.Encoder) error {
	// Get all the jobs
	jobs, err := s.snap.Jobs()
	if err != nil {
		return err
	}

	for {
		// Get the next item
		raw := jobs.Next()
		if raw == nil {
			break
		}

		// Prepare the request struct
		job := raw.(*structs.Job)

		// Write out a job registration
		sink.Write([]byte{byte(JobSnapshot)})
		if err := encoder.Encode(job); err != nil {
			return err
		}
	}
	return nil
}

// Release is a no-op, as we just need to GC the pointer
// to the state store snapshot. There is nothing to explicitly
// cleanup.
func (s *nomadSnapshot) Release() {}
