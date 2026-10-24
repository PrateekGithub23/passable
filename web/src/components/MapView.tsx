"use client";

// MapView: the MapLibre map. It draws the start/end markers and the route, and reports
// clicks to the parent (Planner), which owns all state. MapView itself holds no app state;
// it only mirrors the props it gets onto the imperative MapLibre API.
//
// Why MapLibre is loaded with a dynamic import inside useEffect: MapLibre touches `window`
// and WebGL as soon as it runs, and Next.js prerenders components on the server where neither
// exists. Importing it inside the effect means it only ever loads in the browser.

import { useEffect, useRef, useState } from "react";
import type { GeoJSONSource, Map as MapLibreMap, Marker } from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";

import type { LngLat, RouteResponse } from "@/lib/api";
import { BLOCKED_COLOR, CONFIDENCE_COLOR } from "@/lib/style";

// Free OpenStreetMap vector basemap; no API key needed (https://openfreemap.org).
const BASEMAP_STYLE = "https://tiles.openfreemap.org/styles/liberty";

// The area we have routing data for (south, west, north, east from CLAUDE.md), as
// [[west, south], [east, north]]. The camera is limited to a slightly larger box so users
// cannot wander off to places where every click would fail.
const SERVICE_AREA: [LngLat, LngLat] = [[-80.565, 43.455], [-80.505, 43.485]];
const CAMERA_LIMIT: [LngLat, LngLat] = [[-80.6, 43.44], [-80.47, 43.5]];
const UW_CAMPUS: LngLat = [-80.5425, 43.4705];

interface Props {
  start?: LngLat;
  end?: LngLat;
  route?: RouteResponse;
  onMapClick: (point: LngLat) => void;
}

export default function MapView({ start, end, route, onMapClick }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const markersRef = useRef<{ start?: Marker; end?: Marker }>({});
  // The Marker class arrives with the dynamic import, so keep a handle to it for later effects.
  const markerClassRef = useRef<typeof Marker | null>(null);
  // Sources and layers can only be added once the style has loaded; effects wait on this.
  const [ready, setReady] = useState(false);

  // Keep the latest click handler in a ref. The map's click listener is registered once, and
  // reading the ref (instead of capturing `onMapClick`) avoids calling a stale handler.
  const onClickRef = useRef(onMapClick);
  useEffect(() => {
    onClickRef.current = onMapClick;
  }, [onMapClick]);

  // 1) Create the map once, on mount; destroy it on unmount.
  useEffect(() => {
    let cancelled = false;
    let map: MapLibreMap | undefined;

    (async () => {
      const maplibre = await import("maplibre-gl");
      if (cancelled || !containerRef.current) return;
      // MapLibre parses tiles in a Web Worker. The worker script is copied to /public by the
      // `predev`/`prebuild` npm scripts, because bundlers cannot discover it on their own.
      maplibre.setWorkerUrl("/maplibre-gl-worker.mjs");
      markerClassRef.current = maplibre.Marker;

      map = new maplibre.Map({
        container: containerRef.current,
        style: BASEMAP_STYLE,
        center: UW_CAMPUS,
        zoom: 15,
        maxBounds: CAMERA_LIMIT,
      });
      map.addControl(new maplibre.NavigationControl({ showCompass: false }), "top-right");
      map.on("click", (e) => onClickRef.current([e.lngLat.lng, e.lngLat.lat]));

      map.on("load", () => {
        if (!map) return;
        addRouteLayers(map);
        mapRef.current = map;
        setReady(true);
      });
    })();

    return () => {
      cancelled = true;
      map?.remove();
      mapRef.current = null;
    };
  }, []);

  // 2) Mirror the start/end props onto two markers (created lazily, moved when props change).
  useEffect(() => {
    const map = mapRef.current;
    const MarkerClass = markerClassRef.current;
    if (!ready || !map || !MarkerClass) return;
    for (const which of ["start", "end"] as const) {
      const pos = which === "start" ? start : end;
      let marker = markersRef.current[which];
      if (!pos) {
        marker?.remove();
        markersRef.current[which] = undefined;
        continue;
      }
      if (!marker) {
        marker = new MarkerClass({ color: which === "start" ? "#16a34a" : "#111827" });
        markersRef.current[which] = marker;
      }
      marker.setLngLat(pos).addTo(map);
    }
  }, [ready, start, end]);

  // 3) Draw the route: one GeoJSON line feature per segment, coloured by confidence.
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map) return;
    const segments = map.getSource<GeoJSONSource>("route");
    const barriers = map.getSource<GeoJSONSource>("barriers");
    segments?.setData({
      type: "FeatureCollection",
      features: (route?.segments ?? []).map((s) => ({
        type: "Feature",
        geometry: { type: "LineString", coordinates: s.coordinates },
        properties: {
          // Data-driven styling: the layers read these properties (see addRouteLayers).
          color: s.blocked ? BLOCKED_COLOR : CONFIDENCE_COLOR[s.confidence],
          dashed: s.confidence === "unknown" && !s.blocked,
        },
      })),
    });
    barriers?.setData({
      type: "FeatureCollection",
      features: (route?.barriers ?? []).map((b) => ({
        type: "Feature",
        geometry: { type: "Point", coordinates: b.point },
        properties: {},
      })),
    });

    // Zoom to the whole route when a new one arrives.
    if (route && route.segments.length > 0) {
      const coords = route.segments.flatMap((s) => s.coordinates);
      const lngs = coords.map((c) => c[0]);
      const lats = coords.map((c) => c[1]);
      map.fitBounds(
        [
          [Math.min(...lngs), Math.min(...lats)],
          [Math.max(...lngs), Math.max(...lats)],
        ],
        { padding: 80, maxZoom: 18, duration: 600 },
      );
    }
  }, [ready, route]);

  // 4) Snap connectors. A separate effect from (3) so that moving a point does not re-run
  // fitBounds; it only redraws these two short lines.
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map) return;
    const connectors = map.getSource<GeoJSONSource>("snap-connectors");

    // The route starts on the nearest usable path, which can be some metres from where the
    // user clicked (e.g. the nearest path is steps). A thin dotted line from each click to the
    // route's real start/end makes that gap visible instead of looking like a bug.
    connectors?.setData({
      type: "FeatureCollection",
      features:
        route && start && end
          ? [
              [start, route.snap.from.point],
              [end, route.snap.to.point],
            ].map((coordinates) => ({
              type: "Feature" as const,
              geometry: { type: "LineString" as const, coordinates },
              properties: {},
            }))
          : [],
    });
  }, [ready, route, start, end]);

  return <div ref={containerRef} className="h-full w-full" aria-label="Map. Click to set start and destination." />;
}

// Register the empty sources and the layers that draw them. Later updates only call
// setData() on the sources; the layers' styling rules pick up the new features automatically.
function addRouteLayers(map: MapLibreMap) {
  const empty = { type: "FeatureCollection" as const, features: [] };
  map.addSource("route", { type: "geojson", data: empty });
  map.addSource("barriers", { type: "geojson", data: empty });
  map.addSource("snap-connectors", { type: "geojson", data: empty });
  map.addSource("service-area", {
    type: "geojson",
    data: {
      type: "Feature",
      properties: {},
      geometry: {
        type: "LineString",
        coordinates: [
          SERVICE_AREA[0],
          [SERVICE_AREA[1][0], SERVICE_AREA[0][1]],
          SERVICE_AREA[1],
          [SERVICE_AREA[0][0], SERVICE_AREA[1][1]],
          SERVICE_AREA[0],
        ],
      },
    },
  });

  // Thin outline of where routing data exists.
  map.addLayer({
    id: "service-area",
    type: "line",
    source: "service-area",
    paint: { "line-color": "#6b7280", "line-width": 1.5, "line-dasharray": [4, 3] },
  });
  // Dotted grey lines from each clicked point to where the route actually starts/ends.
  map.addLayer({
    id: "snap-connectors",
    type: "line",
    source: "snap-connectors",
    paint: { "line-color": "#374151", "line-width": 3, "line-dasharray": [1, 1] },
  });
  // A wide white "casing" under the route keeps it readable over any basemap colour.
  map.addLayer({
    id: "route-casing",
    type: "line",
    source: "route",
    layout: { "line-cap": "round", "line-join": "round" },
    paint: { "line-color": "#ffffff", "line-width": 9 },
  });
  // Two route layers, because line-dasharray cannot be switched per feature: one draws the
  // solid segments, the other the dashed (unknown) ones. ["get", "color"] reads the feature's
  // property, so each segment gets its own colour.
  map.addLayer({
    id: "route-solid",
    type: "line",
    source: "route",
    filter: ["==", ["get", "dashed"], false],
    layout: { "line-cap": "round", "line-join": "round" },
    paint: { "line-color": ["get", "color"], "line-width": 5 },
  });
  map.addLayer({
    id: "route-dashed",
    type: "line",
    source: "route",
    filter: ["==", ["get", "dashed"], true],
    layout: { "line-join": "round" },
    paint: { "line-color": ["get", "color"], "line-width": 5, "line-dasharray": [1.5, 1] },
  });
  map.addLayer({
    id: "barriers",
    type: "circle",
    source: "barriers",
    paint: {
      "circle-radius": 8,
      "circle-color": BLOCKED_COLOR,
      "circle-stroke-color": "#ffffff",
      "circle-stroke-width": 2,
    },
  });
}
