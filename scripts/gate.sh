#!/usr/bin/env bash
# Coverage gate: count accessibility-relevant OSM tags in the bbox before building anything on them.
# Usage: scripts/gate.sh            (OVERPASS=<mirror url> to override the endpoint)
set -euo pipefail

OVERPASS="${OVERPASS:-https://overpass-api.de/api/interpreter}"
BBOX="43.455,-80.565,43.485,-80.505" # south,west,north,east
PED='["highway"~"^(footway|path|pedestrian)$"]'

# count <element filter>: prints the total element count for the filter in the bbox.
# "out count;" makes Overpass return only totals, not the data, so each query is tiny.
# Retries handle Overpass rate limiting (HTTP 429/504), which is common on the public server.
count() {
  local q="[out:json][timeout:90];$1($BBOX);out count;"
  local body attempt
  for attempt in 1 2 3; do
    if body=$(curl -sf --max-time 120 -A "passable-gate/0.1" --data-urlencode "data=$q" "$OVERPASS"); then
      echo "$body" | jq -r '.elements[0].tags.total'
      return
    fi
    echo "  (request failed, retry $attempt in 30s)" >&2
    sleep 30
  done
  echo "ERROR: Overpass query failed: $q" >&2
  exit 1
}

row() { printf '%-42s %8s\n' "$1" "$2"; }

# Each count is printed as it arrives; the 2 s sleeps keep us under Overpass's rate limit.
echo "Overpass: $OVERPASS"
echo "BBox:     $BBOX"
echo

ped=$(count "way$PED");                         row "footway/path/pedestrian ways" "$ped"; sleep 2
ped_surface=$(count "way$PED[\"surface\"]");    row "  ... with surface" "$ped_surface"; sleep 2
sidewalk=$(count 'way["footway"="sidewalk"]');  row "footway=sidewalk ways" "$sidewalk"; sleep 2
crossing_w=$(count 'way["footway"="crossing"]'); row "footway=crossing ways" "$crossing_w"; sleep 2
crossing_n=$(count 'node["highway"="crossing"]'); row "highway=crossing nodes" "$crossing_n"; sleep 2
steps=$(count 'way["highway"="steps"]');        row "highway=steps ways" "$steps"; sleep 2
wheelchair=$(count "way$PED[\"wheelchair\"]");  row "ped ways with wheelchair" "$wheelchair"; sleep 2
smooth=$(count "way$PED[\"smoothness\"]");      row "ped ways with smoothness" "$smooth"; sleep 2
incline=$(count "way$PED[\"incline\"]");        row "ped ways with incline" "$incline"; sleep 2
kerb=$(count 'node["kerb"]');                   row "nodes with kerb" "$kerb"; sleep 2
elevator=$(count 'node["highway"="elevator"]'); row "elevator nodes (highway=elevator)" "$elevator"

echo
if [ "$ped" -gt 0 ]; then
  pct() { awk -v n="$1" -v d="$ped" 'BEGIN { printf "%.1f%%", 100 * n / d }'; }
  row "surface coverage (ped ways)" "$(pct "$ped_surface")"
  row "smoothness coverage (ped ways)" "$(pct "$smooth")"
  row "wheelchair coverage (ped ways)" "$(pct "$wheelchair")"
  row "incline coverage (ped ways)" "$(pct "$incline")"
fi
