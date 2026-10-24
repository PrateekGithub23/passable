#!/usr/bin/env bash
# Download the raw OSM data for the bbox into data/: ways with geometry, kerb/crossing nodes, named places.
# Usage: scripts/fetch_osm.sh       (OVERPASS=<mirror url> to override the endpoint)
set -euo pipefail

OVERPASS="${OVERPASS:-https://overpass-api.de/api/interpreter}"
BBOX="43.455,-80.565,43.485,-80.505" # south,west,north,east
OUT="$(cd "$(dirname "$0")/.." && pwd)/data"
mkdir -p "$OUT"

HIGHWAYS="footway|path|pedestrian|steps|living_street|residential|service|unclassified|tertiary|secondary|primary|cycleway|track"

# fetch <query> <file>: POST the query, retrying on rate limits, and write the JSON response.
# It writes to <file>.tmp first and renames on success, so a failed download never leaves
# a half-written file that the importer would then choke on.
fetch() {
  local attempt
  for attempt in 1 2 3; do
    if curl -sf --max-time 300 -A "passable-fetch/0.1" --data-urlencode "data=$1" "$OVERPASS" -o "$2.tmp"; then
      mv "$2.tmp" "$2"
      return
    fi
    echo "  (request failed, retry $attempt in 30s)" >&2
    sleep 30
  done
  rm -f "$2.tmp"
  echo "ERROR: Overpass query failed for $2" >&2
  exit 1
}

# 1) Every way someone could walk on, with full geometry ("out geom" includes each node's
#    coordinates, so no second query for nodes is needed). Excludes foot=no and private access.
# Negated filters (!=, !~) also match ways that lack the key, which is what we want.
fetch "[out:json][timeout:180];
way[\"highway\"~\"^($HIGHWAYS)\$\"][\"foot\"!=\"no\"][\"access\"!~\"^(private|no)\$\"]($BBOX);
out geom;" "$OUT/ways.json"
sleep 2

# 2) Nodes with kerb or crossing tags; the importer attaches kerb values to edges by node id.
fetch "[out:json][timeout:180];
(node[\"kerb\"]($BBOX); node[\"highway\"=\"crossing\"]($BBOX););
out;" "$OUT/nodes.json"
sleep 2

# Named places for search; "out center" gives ways and relations a single point.
fetch "[out:json][timeout:180];
(nwr[\"name\"][\"building\"]($BBOX); nwr[\"name\"][\"amenity\"]($BBOX); nwr[\"name\"][\"shop\"]($BBOX);
 nwr[\"name\"][\"leisure\"]($BBOX); nwr[\"name\"][\"tourism\"]($BBOX); nwr[\"name\"][\"office\"]($BBOX););
out center tags;" "$OUT/places.json"

# Sanity check: an empty file means the query or bbox is wrong, so fail loudly.
for f in ways nodes places; do
  n=$(jq '.elements | length' "$OUT/$f.json")
  printf '%-12s %8s elements  (%s)\n' "$f.json" "$n" "$(du -h "$OUT/$f.json" | cut -f1)"
  [ "$n" -gt 0 ] || { echo "ERROR: $f.json is empty; check the query or bbox" >&2; exit 1; }
done
