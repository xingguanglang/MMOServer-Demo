# Design: Nine-Grid AOI

This document explains the Area-of-Interest (AOI) system: the problem it solves,
why a nine-grid approach was chosen, how enter/leave events are computed, the
alternatives considered, and the pitfalls hit while building it.

Source: [`internal/aoi/aoi.go`](../internal/aoi/aoi.go),
tests: [`internal/aoi/aoi_test.go`](../internal/aoi/aoi_test.go).

## The problem

A map may hold hundreds or thousands of players, but each one only needs to know
about the handful of players near them. Broadcasting every player's movement to
everyone is `O(n²)` work per frame and saturates both CPU and bandwidth within a
few hundred players.

AOI answers one question efficiently: **for a given player, which other players
are close enough to matter (and therefore must be synced)?**

## The nine-grid approach

The map is divided into square cells. Each player lives in exactly one cell. By
convention, a player can only see players in its **own cell plus the 8
surrounding cells** — nine cells total.

```
┌─────┬─────┬─────┐
│  ↖  │  ↑  │  ↗  │   P sees everyone in these 9 cells.
├─────┼─────┼─────┤   Anything outside is invisible to P.
│  ←  │  P  │  →  │
├─────┼─────┼─────┤
│  ↙  │  ↓  │  ↘  │
└─────┴─────┴─────┘
```

Finding "who is near me" becomes a lookup of 9 cells rather than a scan of all
players. In this project the map is 256×256 with a cell size of 32, giving an
8×8 grid (64 cells).

### Choosing the cell size

The rule of thumb is **cell size ≈ player view radius**, so that the 3×3 block
of cells roughly equals the view diameter.

- **Too small** — the real view extends beyond the 9 cells, so nine cells are no
  longer enough; you'd need a 5×5 or larger neighborhood and more complex logic.
- **Too large** — the 9 cells cover far more area than the actual view, so you
  sync players the user can't see, inflating bandwidth and defeating the point
  of AOI.

## Enter/leave: computing view changes

The key optimization: **view changes only occur when a player crosses a cell
boundary.** While a player moves inside the same cell, the set of nearby players
is unchanged, so we do nothing.

When a player crosses from cell A to cell B:

- `entered` = (players in B's 9 cells) − (players in A's 9 cells)
- `left`    = (players in A's 9 cells) − (players in B's 9 cells)

Both sets exclude the moving player itself.

### Visibility is mutual

If P can see Q, then Q can see P. This follows from the grid neighbor relation
being symmetric: if Q's cell is a neighbor of P's cell, then P's cell is a
neighbor of Q's cell. So every enter/leave event is delivered to **both** sides:
when Q enters P's view, P simultaneously enters Q's view. The scene layer uses
this to drive paired "appear/disappear" notifications from a single computation.

### Complexity

- Cell lookup for a position: `O(1)`.
- Gathering nearby players / a view-change diff: `O(k)`, where `k` is the number
  of players in the 9 relevant cells — effectively constant for a roughly uniform
  distribution.

## Separation of concerns

The AOI manager is **pure**: it imports no networking or protocol code and only
returns player-id lists. Sending the actual messages is the scene/gateway's job.
This keeps the AOI logic unit-testable in isolation and is why
`internal/aoi/aoi.go` has no dependencies beyond the standard library.

One consequence worth noting: `ViewPlayers(x, y)` is a *position* query and
includes whoever is standing at that position (e.g. the querying player).
Excluding "self" is the caller's responsibility — consistent with how the
gateway already excludes the sender when broadcasting.

## Alternatives considered

| Approach            | Idea                                        | Pros                                | Cons                                          |
| ------------------- | ------------------------------------------- | ----------------------------------- | --------------------------------------------- |
| Brute force `O(n²)` | Each player checks distance to all others   | Trivial to implement                | Collapses past a few hundred players          |
| **Nine-grid**       | Partition map; only look at 9 cells          | Simple, `O(1)` neighbor lookup       | Fixed cell size; degrades if players cluster  |
| Cross-linked list   | Two sorted lists (by X, by Y); intersect    | Arbitrary view radius; uneven dist. | More bookkeeping on movement                  |
| Quadtree            | Recursive spatial subdivision by density    | Great for huge, sparse worlds       | More complex; rebalancing                     |

Nine-grid was chosen because it is the simplest to implement and explain, has
`O(1)` neighbor lookup, and is well-suited to a roughly uniform demo world. Its
main weakness — clustering (everyone in one cell) degrading back toward `O(n²)`
— is a known trade-off; the production answer is sharding scenes by region
rather than tuning a single grid.

## Pitfalls hit while building this

- **Off-by-one in the neighbor scan.** The surrounding-cell loop must use
  `<= row+1` / `<= col+1`. Using `<` silently yields a 2×2 block (top-left
  quadrant) instead of the full 3×3, so players to the right/below are never
  seen. The cross-cell enter test caught this.
- **`ViewPlayers` includes the query position's occupant.** Because it's a
  position query, it returns the player standing there too. Tests and the
  broadcast path must exclude self explicitly.
- **Dead duplicate `return`.** A leftover second `return entered, left` in
  `Move` was unreachable; `go vet` flagged it. Keep `go vet ./...` in the loop.

## Related

- Move broadcasting and enter/leave wiring live in
  [`internal/scene/scene.go`](../internal/scene/scene.go).
- The gateway translates view events into `PlayerEnter` / `PlayerLeave` /
  `MoveBroadcast` wire messages in
  [`internal/gateway/server.go`](../internal/gateway/server.go).
