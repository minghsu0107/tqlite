# TQLite
*tqlite* is a distributed SQL database with replication, fault-tolerance, tunable consistency and leader election. It uses [SQLite](https://www.sqlite.org/index.html), a small, fast and self-contained SQL engine, as the basic unit in the cluster.
## Motivation
SQLite is a popular embedded SQL database. It is lightweight, full-featured, and easy to use. However, it is prone to single-point-of-failure due to its single-file-based nature.

tqlite provides you a lightweight, reliable and highly available SQL cluster, with **easy deployment, and operation**. Think of tqlite as a SQL version of [etcd](https://github.com/coreos/etcd/) or [Consul](https://github.com/hashicorp/consul).

## How it works
tqlite ensures the system state is in accordance with a quorum of nodes in the cluster using [Raft](https://raft.github.io/), a well-kown concensus algorithm in a distributed system.
## Key features
- Lightweight deployment with a single binary
- Support dumping, backing up, and restoring database
- Straightforward HTTP data API
- Distributed consensus system
- Tunable read consistency
## Quick start
### Installation
Docker container is available:
```bash
docker pull minghsu0107/tqlite:v1
```
Or you could build from source:
```bash
git clone https://github.com/minghsu0107/tqlite.git
go build -o tqlite -v ./cmd/tqlite
go build -o tqlited -v ./cmd/tqlited
```
### Running first node
You can start a single tqlite node first:
```bash
docker network create tqlite-net
docker run --name node1 -p 4001:4001 --network tqlite-net minghsu0107/tqlite:v1 -node-id 1 -http-addr 0.0.0.0:4001 -raft-addr node1:4002
```

This single node becomes the leader automatically. You can pass `-h` to `tqlited` to list all configuration options.
### Joining a cluster
To be fault-tolerant, we could run tqlite in the cluster mode. For example, we could join the second and third node to the cluster by simply running:
```bash
docker run --name node2 -p 4002:4001 --network tqlite-net minghsu0107/tqlite:v1 -node-id 2 -http-addr 0.0.0.0:4001 -raft-addr node2:4002 -join http://node1:4001

docker run --name node3 -p 4003:4001 --network tqlite-net minghsu0107/tqlite:v1 -node-id 3 -http-addr 0.0.0.0:4001 -raft-addr node3:4002 -join http://node1:4001
```
Now you have a fully replicated cluster where a majority, or a quorum, of nodes are required to reach conensus on any change to the cluster state. A quorum is is defined as `(N/2)+1` where N is the number of nodes in the cluster. In this example, a 3-node cluster is able to tolerate a single node failure.
### Using client CLI
Now, we are going to use tqlite client CLI to insert some data to the leader node. The leader will then replicate data to all followers within the cluster.
```bash
docker exec -it node1 bash
tqlite
```
```
$ tqlite
127.0.0.1:4001> CREATE TABLE students (id INTEGER NOT NULL PRIMARY KEY, name TEXT);
0 row affected
127.0.0.1:4001> .schema
+--------------------------------------------------------------------+
| sql                                                                |
+--------------------------------------------------------------------+
| CREATE TABLE students (id INTEGER NOT NULL PRIMARY KEY, name TEXT) |
+--------------------------------------------------------------------+
127.0.0.1:4001> INSERT INTO students(name) VALUES("ming");
1 row affected
127.0.0.1:4001> SELECT * FROM students;
+----+------+
| id | name |
+----+------+
| 1  | ming |
+----+------+
```
You can see that tqlite client CLI is compatible with SQLite, minimizing the operation costs.
## Data API
Inspired by Elasticsearch, tqlite exposes data by a rich HTTP API, allowing full control over nodes to query from or write to. We could use HTTP API to do CRUD operations with tunable consistency. Take above `students` table as an example:
```bash
# query
curl -XPOST 'localhost:4001/db/query?pretty&timings' -H "Content-Type: application/json" -d '[
    "SELECT * FROM students"
]'
```
Query result:
```
{
    "results": [
        {
            "columns": [
                "id",
                "name"
            ],
            "types": [
                "integer",
                "text"
            ],
            "values": [
                [
                    1,
                    "ming"
                ]
            ],
            "time": 0.000053034
        }
    ],
    "time": 0.000098828
}
```

In addition, you could pass parameterized statements to avoid SQL injections:
```bash
# write
curl -XPOST 'localhost:4001/db/execute?pretty&timings' -H "Content-Type: application/json" -d '[
    ["INSERT INTO students(name) VALUES(?)", "alice"]
]'
# read
curl -XPOST 'localhost:4001/db/query?pretty&timings' -H "Content-Type: application/json" -d '[
    ["SELECT * FROM students WHERE name=?", "alice"]
]'
```
You could start a transaction by adding `transaction` query parameter:
```bash
curl -XPOST 'localhost:4001/db/execute?pretty&transaction' -H "Content-Type: application/json" -d "[
    \"INSERT INTO students(name) VALUES('alan')\",
    \"INSERT INTO students(name) VALUES('monica')\"
]"
```
Multiple insertions or updates in a transaction are contained within a single Raft log entry and will not be interleaved with other requests.
### Write Consistency
Any write request received by followers will be fowarded to the leader. A write request received by the leader is considered successful once it replicates the data to a quorum of nodes through Raft successfully. In the below command, we send a write request to `node2`, a follower. Thus the request will be redirected to the leader:
```bash
curl -i -XPOST 'localhost:4003/db/execute?pretty&timings' -H "Content-Type: application/json" -d '[
    ["INSERT INTO students(name) VALUES(?)", "bob"]
]'
```
Result:
```
HTTP/1.1 301 Moved Permanently
Content-Type: application/json; charset=utf-8
Location: http://localhost:4001/db/execute?pretty&timings
X-Tqlite-Version: 1
Date: Mon, 07 Jun 2021 17:25:13 GMT
Content-Length: 0
```
Then it is up the clients to re-issue the query command to the leader.
### Read Consistency
As for read operations, query with consistency level `none` will result in a local read. That is, the node simply queries its local SQLite database directly. In HTTP data API, We should set the query string parameter `level` to `none` to enable it:
```bash
curl -i -XPOST 'localhost:4003/db/query?pretty&timings&level=none' -H "Content-Type: application/json" -d '[
    ["SELECT * FROM students WHERE name=?", "alice"]
]'
```
In the above query, we send read request to `node2` and will receive an instant response from `node2` without checking its leadership with other peers in the cluster.

In contrast, if we send read request to the leader node with consistency level set to `strong`, tqlite will sends the request through Raft consensus system, ensuring that the node remains the leader at all times during query processing. If the node receiving the read request is a follower, the request will be redirected to the leader. However, this will involve the leader contacting at least a quorum of nodes, and will therefore increase query response times.

Redirection example:
```bash
curl -i -XPOST 'localhost:4003/db/query?pretty&timings&level=strong' -H "Content-Type: application/json" -d '[
    ["SELECT * FROM students WHERE name=?", "alice"]
]'
```
Result:
```
HTTP/1.1 301 Moved Permanently
Content-Type: application/json; charset=utf-8
Location: http://localhost:4001/db/query?pretty&timings&level=strong
X-Tqlite-Version: 1
Date: Mon, 07 Jun 2021 17:25:57 GMT
Content-Length: 0
```
## In-memory store
To enhance the performance, tqlite runs SQLite [in-memory](https://www.sqlite.org/inmemorydb.html) by default, meaning that there is no actual file created on disk. The data durability is guaranteed by the journal committed by Raft, so the database is able to be recreated in the memory on restart. However, you could still enable the disk mode by adding flag `-on-disk` to `tqlited`.
