## TQLite
*tqlite* is a fault-tolerant distributed SQLite cluster with replication, tunable consistency and leader election. It handles leader elections gracefully using the Raft algorithm, and tolerates failures of any nodes within the cluster, including the leader.

### Motivation
SQLite is a popular embedded SQL database. It is lightweight, full-featured, and easy to use. However, it is prone to single-point-of-failure due to its single-file-based nature.

tqlite provides you a lightweight, replicated and fault-tolerant RDBMS cluster, with **easy deployment, and operation**. Think of tqlite as a SQL version of [etcd](https://github.com/coreos/etcd/) or [Consul](https://github.com/hashicorp/consul).

### How it works
tqlite ensures the system state is in accordance with a quorum of nodes in the cluster using [Raft](https://raft.github.io/), a well-kown concensus algorithm in a distributed system.
### Key features
- Easy deployment without additionally SQLite dependency
- Distributed consensus system
- Command line interface compatible with standard SQLite
- Straightforward HTTP data API
- Tunable read consistency

## Quick Start
You can start a single tqlite node first:
```bash
docker network create tqlite-net
docker run --name node1 -p 4001:4001 --network tqlite-net minghsu0107/tqlite:v1 -node-id 1 -http-addr 0.0.0.0:4001 -raft-addr node1:4002
```

This single node becomes the leader automatically. You can pass `-h` to `tqlited` to list all configuration options.
### Forming a cluster
To be fault-tolerant, we could run tqlite in the cluster mode. For example, we could join the second and third node to the cluster by simply running:
```bash
docker run --name node2 -p 4002:4001 --network tqlite-net minghsu0107/tqlite:v1 -node-id 2 -http-addr 0.0.0.0:4001 -raft-addr node2:4002 -join http://node1:4001

docker run --name node3 -p 4003:4001 --network tqlite-net minghsu0107/tqlite:v1 -node-id 3 -http-addr 0.0.0.0:4001 -raft-addr node3:4002 -join http://node1:4001
```
Now you have a fully replicated cluster where a majority, or a quorum, of nodes are required to reach conensus on any change to the cluster state. A quorum is is defined as `(N/2)+1` where N is the number of nodes in the cluster. In this example, a 3-node cluster is able to tolerate a single node failure.
### Inserting records
Now, we are goint to insert some records to the leader node via tqlite CLI, while using **standard SQLite commands**. Once the insertion suceeds, records wil be replicated accross the entire cluster in a fault tolerant and durable manner. The 3-node cluster we have created is able to tolerant the failure of any single node without any loss of functionality or data.
```bash
docker exec -it node1 bash
tqlite
```
```
$ tqlite
127.0.0.1:4001> CREATE TABLE students (id INTEGER NOT NULL PRIMARY KEY, name TEXT)
0 row affected
127.0.0.1:4001> .schema
+--------------------------------------------------------------------+
| sql                                                                |
+--------------------------------------------------------------------+
| CREATE TABLE students (id INTEGER NOT NULL PRIMARY KEY, name TEXT) |
+--------------------------------------------------------------------+
127.0.0.1:4001> INSERT INTO students(name) VALUES("ming")
1 row affected
127.0.0.1:4001> SELECT * FROM students
+----+------+
| id | name |
+----+------+
| 1  | ming |
+----+------+
```

## Data API
tqlite exposes data by a rich HTTP API, allowing full control over nodes to query from or write to.
### In-memory databases
To maximize the performance, tqlite runs SQLite [in-memory](https://www.sqlite.org/inmemorydb.html), meaning that there is no actual file created on the disk. The data durability is guaranteed by the journal committed by Raft. Thus the in-memory database is able to be recreated on start-up.