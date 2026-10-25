# Demo video: shot list (about 2½ minutes)

The goal is to show the three things that make Passable different: profile-aware routes, live hazard reports that change routes, and that only affected people are prompted. Record at 1280×800 or larger, with two browser windows side by side for the live part.

**Before recording:**
- Open https://passable-alpha.vercel.app in two windows: **left** (the reporter) and **right** (the person walking).
- Confirm https://passable-alpha.vercel.app/status says "All systems operational" and shows 0 active reports (so the map is clean).
- Don't run the simulator. Its "Demo" hazards would distract here.

| # | Time | On screen | Say (roughly) |
|---|---|---|---|
| 1 | 0:00 | Right window, the empty map | "Passable plans walking routes on UW campus for people who can't just take the shortest path." |
| 2 | 0:10 | Right: click **SLC**, then **Hagey Hall**; profile **Wheelchair** | "A wheelchair route avoids steps and raised kerbs. Dark blue means the map data is verified, dashed amber means it's missing. The route admits what it doesn't know." |
| 3 | 0:30 | Switch to **Winter walk**, then back to **Wheelchair** | "Winter walk takes the steps and Ring Road; wheelchair goes through campus on paved paths. Same graph, different cost models." |
| 4 | 0:45 | Scroll the route details list on the right | "Every segment says what it is and why it cost what it did: surface, kerbs, unknowns." |
| 5 | 0:55 | Left: **Report a problem** → tap Two Row Path → **Blocked sidewalk** → **Send** | "Someone finds a fence across the path and reports it in three taps." |
| 6 | 1:10 | Right: the red dot appears, then the **"Blocked sidewalk reported on your route"** prompt | "Within half a second, the person whose route crosses it gets asked to reroute. Nobody else is interrupted: the server keeps an index from each path segment to the people routing over it." |
| 7 | 1:25 | Right: **Reroute** | "The new route goes around on Ring Road. Under the hood the report produced a new immutable snapshot of hazard costs; searches never wait on it." |
| 8 | 1:40 | Left: tap the red dot → **It's gone** | "When it's cleared, the reporter or two other people mark it gone, and it disappears for everyone. Reports also expire on their own." |
| 9 | 1:55 | Open **/status** | "Live numbers from production: latency from real traffic, cache hit rate, and how fast reports reach other people's maps." |
| 10 | 2:10 | Optional: GitHub Actions run, or `go test -race` | "Everything is tested: property tests against Dijkstra, a race-detector stress test of routing during hazard writes, and integration tests with two API instances on real Postgres and Redis." |
| 11 | 2:25 | Back to the map | "Data from OpenStreetMap; the limitations, like no slope data, are listed on the About page." |

**After recording:** check `/status` shows 0 active reports. If a test hazard is left, tap it and choose "It's gone".
