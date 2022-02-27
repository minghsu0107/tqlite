// Command tqlited is the tqlite server.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/minghsu0107/tqlite/cluster"
	"github.com/minghsu0107/tqlite/cmd"
	httpd "github.com/minghsu0107/tqlite/http"
	"github.com/minghsu0107/tqlite/store"
	"github.com/minghsu0107/tqlite/tcp"
)

var httpAddr string
var httpAdv string
var joinSrcIP string
var nodeID string
var raftAddr string
var raftAdv string
var joinAddr string
var joinAttempts int
var joinInterval string
var expvar bool
var pprofEnabled bool
var dsn string
var onDisk bool
var raftLogLevel string
var raftNonVoter bool
var raftSnapThreshold uint64
var raftSnapInterval string
var raftLeaderLeaseTimeout string
var raftHeartbeatTimeout string
var raftElectionTimeout string
var raftApplyTimeout string
var raftOpenTimeout string
var raftWaitForLeader bool
var raftShutdownOnRemove bool
var compressionSize int
var compressionBatch int
var showVersion bool
var cpuProfile string
var memProfile string

const name = `tqlited`
const desc = `tqlite is a lightweight, distributed relational database, which uses SQLite as its
storage engine. It provides an easy-to-use, fault-tolerant store for relational data.`

func init() {
	flag.StringVar(&nodeID, "node-id", "", "Unique name for node. If not set, set to Raft address")
	flag.StringVar(&httpAddr, "http-addr", "localhost:4001", "HTTP server bind address")
	flag.StringVar(&httpAdv, "http-adv-addr", "", "Advertised HTTP address. If not set, same as HTTP server")
	flag.StringVar(&joinSrcIP, "join-source-ip", "", "Set source IP address during Join request")
	flag.StringVar(&raftAddr, "raft-addr", "localhost:4002", "Raft communication bind address")
	flag.StringVar(&raftAdv, "raft-adv-addr", "", "Advertised Raft communication address. If not set, same as Raft bind")
	flag.StringVar(&joinAddr, "join", "", "Comma-delimited list of nodes, through which a cluster can be joined (proto://host:port)")
	flag.IntVar(&joinAttempts, "join-attempts", 5, "Number of join attempts to make")
	flag.StringVar(&joinInterval, "join-interval", "5s", "Period between join attempts")
	flag.BoolVar(&expvar, "expvar", true, "Serve expvar data on HTTP server")
	flag.BoolVar(&pprofEnabled, "pprof", true, "Serve pprof data on HTTP server")
	flag.StringVar(&dsn, "dsn", "", `SQLite DSN parameters. E.g. "cache=shared&mode=memory"`)
	flag.BoolVar(&onDisk, "on-disk", false, "Use an on-disk SQLite database")
	flag.BoolVar(&showVersion, "version", false, "Show version information and exit")
	flag.BoolVar(&raftNonVoter, "raft-non-voter", false, "Configure as non-voting node")
	flag.StringVar(&raftHeartbeatTimeout, "raft-timeout", "1s", "Raft heartbeat timeout")
	flag.StringVar(&raftElectionTimeout, "raft-election-timeout", "1s", "Raft election timeout")
	flag.StringVar(&raftApplyTimeout, "raft-apply-timeout", "10s", "Raft apply timeout")
	flag.StringVar(&raftOpenTimeout, "raft-open-timeout", "120s", "Time for initial Raft logs to be applied. Use 0s duration to skip wait")
	flag.BoolVar(&raftWaitForLeader, "raft-leader-wait", true, "Node waits for a leader before answering requests")
	flag.Uint64Var(&raftSnapThreshold, "raft-snap", 8192, "Number of outstanding log entries that trigger snapshot")
	flag.StringVar(&raftSnapInterval, "raft-snap-int", "30s", "Snapshot threshold check interval")
	flag.StringVar(&raftLeaderLeaseTimeout, "raft-leader-lease-timeout", "0s", "Raft leader lease timeout. Use 0s for Raft default")
	flag.BoolVar(&raftShutdownOnRemove, "raft-remove-shutdown", false, "Shutdown Raft if node removed")
	flag.StringVar(&raftLogLevel, "raft-log-level", "INFO", "Minimum log level for Raft module")
	flag.IntVar(&compressionSize, "compression-size", 150, "Request query size for compression attempt")
	flag.IntVar(&compressionBatch, "compression-batch", 5, "Request batch threshold for compression attempt")
	flag.StringVar(&cpuProfile, "cpu-profile", "", "Path to file for CPU profiling information")
	flag.StringVar(&memProfile, "mem-profile", "", "Path to file for memory profiling information")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n%s\n\n", desc)
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <data directory>\n", name)
		flag.PrintDefaults()
	}
}

func main() {
	flag.Parse()

	if showVersion {
		fmt.Printf("%s %s %s %s %s (commit %s, branch %s)\n",
			name, cmd.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(), cmd.Commit, cmd.Branch)
		os.Exit(0)
	}

	// Ensure the data path is set.
	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "fatal: no data directory set\n")
		os.Exit(1)
	}

	// Ensure no args come after the data directory.
	if flag.NArg() > 1 {
		fmt.Fprintf(os.Stderr, "fatal: arguments after data directory are not accepted\n")
		os.Exit(1)
	}

	dataPath := flag.Arg(0)

	// Configure logging and pump out initial message.
	log.SetFlags(log.LstdFlags)
	log.SetOutput(os.Stderr)
	log.SetPrefix(fmt.Sprintf("[%s] ", name))
	// log.Printf("%s starting, version %s, commit %s, branch %s", name, cmd.Version, cmd.Commit, cmd.Branch)
	// log.Printf("%s, target architecture is %s, operating system target is %s", runtime.Version(), runtime.GOARCH, runtime.GOOS)
	log.Printf("launch command: %s", strings.Join(os.Args, " "))

	// Start requested profiling.
	startProfile(cpuProfile, memProfile)

	// Create internode network mux and configure.
	muxLn, err := net.Listen("tcp", raftAddr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %s", raftAddr, err.Error())
	}
	mux, err := startNodeMux(muxLn)
	if err != nil {
		log.Fatalf("failed to start node mux: %s", err.Error())
	}
	raftTn := mux.Listen(cluster.MuxRaftHeader)

	// Create cluster service, so nodes can learn information about each other. This can be started
	// now since it doesn't require a functioning Store yet.
	clstr, err := clusterService(mux.Listen(cluster.MuxClusterHeader))
	if err != nil {
		log.Fatalf("failed to create cluster service: %s", err.Error())
	}

	// Create and open the store.
	dataPath, err = filepath.Abs(dataPath)
	if err != nil {
		log.Fatalf("failed to determine absolute data path: %s", err.Error())
	}
	dbConf := store.NewDBConfig(dsn, !onDisk)

	str := store.New(raftTn, &store.StoreConfig{
		DBConf: dbConf,
		Dir:    dataPath,
		ID:     idOrRaftAddr(),
	})

	// Set optional parameters on store.
	str.SetRequestCompression(compressionBatch, compressionSize)
	str.RaftLogLevel = raftLogLevel
	str.ShutdownOnRemove = raftShutdownOnRemove
	str.SnapshotThreshold = raftSnapThreshold
	str.SnapshotInterval, err = time.ParseDuration(raftSnapInterval)
	if err != nil {
		log.Fatalf("failed to parse Raft Snapsnot interval %s: %s", raftSnapInterval, err.Error())
	}
	str.LeaderLeaseTimeout, err = time.ParseDuration(raftLeaderLeaseTimeout)
	if err != nil {
		log.Fatalf("failed to parse Raft Leader lease timeout %s: %s", raftLeaderLeaseTimeout, err.Error())
	}
	str.HeartbeatTimeout, err = time.ParseDuration(raftHeartbeatTimeout)
	if err != nil {
		log.Fatalf("failed to parse Raft heartbeat timeout %s: %s", raftHeartbeatTimeout, err.Error())
	}
	str.ElectionTimeout, err = time.ParseDuration(raftElectionTimeout)
	if err != nil {
		log.Fatalf("failed to parse Raft election timeout %s: %s", raftElectionTimeout, err.Error())
	}
	str.ApplyTimeout, err = time.ParseDuration(raftApplyTimeout)
	if err != nil {
		log.Fatalf("failed to parse Raft apply timeout %s: %s", raftApplyTimeout, err.Error())
	}

	// Any prexisting node state?
	var enableBootstrap bool
	isNew := store.IsNewNode(dataPath)
	if isNew {
		log.Printf("no preexisting node state detected in %s, node may be bootstrapping", dataPath)
		enableBootstrap = true // New node, so we may be bootstrapping
	} else {
		log.Printf("preexisting node state detected in %s", dataPath)
	}

	// Determine join addresses
	var joins []string
	joins, err = determineJoinAddresses()
	if err != nil {
		log.Fatalf("unable to determine join addresses: %s", err.Error())
	}

	// Supplying join addresses means bootstrapping a new cluster won't
	// be required.
	if len(joins) > 0 {
		enableBootstrap = false
		log.Println("join addresses specified, node is not bootstrapping")
	} else {
		log.Println("no join addresses set")
	}

	// Join address supplied, but we don't need them!
	if !isNew && len(joins) > 0 {
		log.Println("node is already member of cluster, ignoring join addresses")
	}

	// Now, open store.
	if err := str.Open(enableBootstrap); err != nil {
		log.Fatalf("failed to open store: %s", err.Error())
	}

	// Execute any requested join operation.
	if len(joins) > 0 && isNew {
		log.Println("join addresses are:", joins)
		advAddr := raftAddr
		if raftAdv != "" {
			advAddr = raftAdv
		}

		joinDur, err := time.ParseDuration(joinInterval)
		if err != nil {
			log.Fatalf("failed to parse Join interval %s: %s", joinInterval, err.Error())
		}

		if j, err := cluster.Join(joinSrcIP, joins, str.ID(), advAddr, !raftNonVoter,
			joinAttempts, joinDur); err != nil {
			log.Fatalf("failed to join cluster at %s: %s", joins, err.Error())
		} else {
			log.Println("successfully joined cluster at", j)
		}

	}

	// Wait until the store is in full consensus.
	if err := waitForConsensus(str); err != nil {
		log.Fatalf(err.Error())
	}
	log.Println("store has reached consensus")

	// Start the HTTP API server.
	if err := startHTTPService(str, clstr); err != nil {
		log.Fatalf("failed to start HTTP server: %s", err.Error())
	}
	log.Println("node is ready")

	// Block until signalled.
	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, os.Interrupt)
	<-terminate
	if err := str.Close(true); err != nil {
		log.Printf("failed to close store: %s", err.Error())
	}
	clstr.Close()
	muxLn.Close()
	stopProfile()
	log.Println("tqlite server stopped")
}

func determineJoinAddresses() ([]string, error) {
	var addrs []string
	if joinAddr != "" {
		// Explicit join addresses are first priority.
		addrs = strings.Split(joinAddr, ",")
	}

	return addrs, nil
}

func waitForConsensus(str *store.Store) error {
	openTimeout, err := time.ParseDuration(raftOpenTimeout)
	if err != nil {
		return fmt.Errorf("failed to parse Raft open timeout %s: %s", raftOpenTimeout, err.Error())
	}
	if _, err := str.WaitForLeader(openTimeout); err != nil {
		if raftWaitForLeader {
			return fmt.Errorf("leader did not appear within timeout: %s", err.Error())
		}
		log.Println("ignoring error while waiting for leader")
	}
	if openTimeout != 0 {
		if err := str.WaitForApplied(openTimeout); err != nil {
			return fmt.Errorf("log was not fully applied within timeout: %s", err.Error())
		}
	} else {
		log.Println("not waiting for logs to be applied")
	}
	return nil
}

func startHTTPService(str *store.Store, cltr *cluster.Service) error {
	// Create HTTP server
	var s *httpd.Service
	s = httpd.New(httpAddr, str, cltr)

	s.Expvar = expvar
	s.Pprof = pprofEnabled
	s.BuildInfo = map[string]interface{}{
		"commit":     cmd.Commit,
		"branch":     cmd.Branch,
		"version":    cmd.Version,
		"build_time": cmd.Buildtime,
	}
	return s.Start()
}

func startNodeMux(ln net.Listener) (*tcp.Mux, error) {
	var adv net.Addr
	var err error
	if raftAdv != "" {
		adv, err = net.ResolveTCPAddr("tcp", raftAdv)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve advertise address %s: %s", raftAdv, err.Error())
		}
	}

	var mux *tcp.Mux
	mux, err = tcp.NewMux(ln, adv)
	if err != nil {
		return nil, fmt.Errorf("failed to create node-to-node mux: %s", err.Error())
	}

	go mux.Serve()

	return mux, nil
}

func clusterService(tn cluster.Transport) (*cluster.Service, error) {
	c := cluster.New(tn)
	apiAddr := httpAddr
	if httpAdv != "" {
		apiAddr = httpAdv
	}
	c.SetAPIAddr(apiAddr)

	if err := c.Open(); err != nil {
		return nil, err
	}
	return c, nil
}

func idOrRaftAddr() string {
	if nodeID != "" {
		return nodeID
	}
	if raftAdv == "" {
		return raftAddr
	}
	return raftAdv
}

// prof stores the file locations of active profiles.
var prof struct {
	cpu *os.File
	mem *os.File
}

// startProfile initializes the CPU and memory profile, if specified.
func startProfile(cpuprofile, memprofile string) {
	if cpuprofile != "" {
		f, err := os.Create(cpuprofile)
		if err != nil {
			log.Fatalf("failed to create CPU profile file at %s: %s", cpuprofile, err.Error())
		}
		log.Printf("writing CPU profile to: %s\n", cpuprofile)
		prof.cpu = f
		pprof.StartCPUProfile(prof.cpu)
	}

	if memprofile != "" {
		f, err := os.Create(memprofile)
		if err != nil {
			log.Fatalf("failed to create memory profile file at %s: %s", cpuprofile, err.Error())
		}
		log.Printf("writing memory profile to: %s\n", memprofile)
		prof.mem = f
		runtime.MemProfileRate = 4096
	}
}

// stopProfile closes the CPU and memory profiles if they are running.
func stopProfile() {
	if prof.cpu != nil {
		pprof.StopCPUProfile()
		prof.cpu.Close()
		log.Println("CPU profiling stopped")
	}
	if prof.mem != nil {
		pprof.Lookup("heap").WriteTo(prof.mem, 0)
		prof.mem.Close()
		log.Println("memory profiling stopped")
	}
}
