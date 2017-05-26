---
layout: api
page_title: Status - HTTP API
sidebar_current: api-status
description: |-
  The /status endpoints query the Nomad system status.
---

# Status HTTP API

The `/status` endpoints query the Nomad system status.

## Read Leader

This endpoint returns the address of the current leader in the region.

| Method | Path                         | Produces                   |
| ------ | ---------------------------- | -------------------------- |
| `GET`  | `/status/leader`             | `application/json`         |

The table below shows this endpoint's support for
[blocking queries](/api/index.html#blocking-queries),
[consistency modes](/api/index.html#consistency-modes), and
[required ACLs](/api/index.html#acls).

| Blocking Queries | Consistency Modes | ACL Required |
| ---------------- | ----------------- | ------------ |
| `NO`             | `all`             | `none`       |

### Sample Request

```text
$ curl \
    https://nomad.rocks/v1/status/leader
```

### Sample Response

```json
"127.0.0.1:4647"
```

## List Peers

This endpoint returns the set of raft peers in the region.

| Method | Path                         | Produces                   |
| ------ | ---------------------------- | -------------------------- |
| `GET`  | `/status/peers`              | `application/json`         |

The table below shows this endpoint's support for
[blocking queries](/api/index.html#blocking-queries),
[consistency modes](/api/index.html#consistency-modes), and
[required ACLs](/api/index.html#acls).

| Blocking Queries | Consistency Modes | ACL Required |
| ---------------- | ----------------- | ------------ |
| `NO`             | `all`             | `none`       |

### Sample Request

```text
$ curl \
    https://nomad.rocks/v1/status/peers
```

### Sample Response

```json
[
  "127.0.0.1:4647"
]
```
