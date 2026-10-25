"use client";

// MapView: the MapLibre map. It draws the start/end markers, the route and live hazard
// markers, and reports clicks and the visible area to the parent (Planner), which owns all
// state. MapView itself holds no app state; it only mirrors its props onto the imperative
// MapLibre API.
//
// Why MapLibre is loaded with a dynamic import inside useEffect: MapLibre touches `window`
// and WebGL as soon as it runs, and Next.js prerenders components on the server where neither
// exists. Importing it inside the effect means it only ever loads in the browser.

import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { GeoJSONSource, Map as MapLibreMap, Marker, Popup } from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";

import type { BBox, Hazard, LngLat, RouteResponse } from "@/lib/api";
import { BLOCKED_COLOR, CONFIDENCE_COLOR, HAZARD_STYLE } from "@/lib/style";
import HazardPopup from "./HazardPopup";

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
  hazards: Hazard[];
  /** Report mode: clicks pick the problem's location (and don't open hazard popups). */
  reporting: boolean;
  /** Where the user tapped in report mode; shown as a marker while the form is open. */
  reportPoint?: LngLat;
  /** Pan here (e.g. a place picked from search); a new nonce pans again to the same place. */
  focus?: { point: LngLat; nonce: number };
  onMapClick: (point: LngLat) => void;
  onViewportChange: (bbox: BBox) => void;
  /** Vote from a hazard popup; resolves with the updated report. */
  onVote: (id: number, kind: "confirm" | "resolve") => Promise<Hazard>;
}

export default function MapView({ start, end, route, hazards, reporting, reportPoint, focus, onMapClick, onViewportChange, onVote }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const markersRef = useRef<{ start?: Marker; end?: Marker; report?: Marker }>({});
  // The Marker class arrives with the dynamic import, so keep a handle to it for later effects.
  const markerClassRef = useRef<typeof Marker | null>(null);
  const popupClassRef = useRef<typeof Popup | null>(null);
  // Hazards by id, so a click on a marker can show the full report in a popup.
  const hazardsByIdRef = useRef<Map<number, Hazard>>(new Map());
  // Sources and layers can only be added once the style has loaded; effects wait on this.
  const [ready, setReady] = useState(false);

  // Keep the latest click handler in a ref. The map's click listener is registered once, and
  // reading the ref (instead of capturing `onMapClick`) avoids calling a stale handler.
  const onClickRef = useRef(onMapClick);
  const onViewportRef = useRef(onViewportChange);
  const onVoteRef = useRef(onVote);
  const reportingRef = useRef(reporting);
  useEffect(() => {
    onClickRef.current = onMapClick;
    onViewportRef.current = onViewportChange;
    onVoteRef.current = onVote;
    reportingRef.current = reporting;
  }, [onMapClick, onViewportChange, onVote, reporting]);

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
      popupClassRef.current = maplibre.Popup;

      map = new maplibre.Map({
        container: containerRef.current,
        style: BASEMAP_STYLE,
        center: UW_CAMPUS,
        zoom: 15,
        maxBounds: CAMERA_LIMIT,
        // Compact attribution (an (i) button that expands on tap). The full text is required
        // credit for OSM/OpenFreeMap, but expanded it covers the "Report a problem" button on phones.
        attributionControl: { compact: true },
      });
      map.addControl(new maplibre.NavigationControl({ showCompass: false }), "top-right");
      map.on("click", (e) => {
        if (!map) return;
        // A click on a hazard marker opens its details instead of setting a route point
        // (except in report mode, where every click means "the problem is here").
        const hit = !reportingRef.current && map.getLayer("hazards") ? map.queryRenderedFeatures(e.point, { layers: ["hazards"] }) : [];
        const h = hit.length > 0 ? hazardsByIdRef.current.get(hit[0].properties.id) : undefined;
        if (h && popupClassRef.current) {
          // Render the React card into a detached element and hand that to MapLibre; unmount
          // it when the popup closes so nothing leaks.
          const el = document.createElement("div");
          const root = createRoot(el);
          root.render(<HazardPopup hazard={h} onVote={(kind) => onVoteRef.current(h.id, kind)} />);
          const popup = new popupClassRef.current({ offset: 12, maxWidth: "none" }).setLngLat(h.point).setDOMContent(el).addTo(map);
          popup.on("close", () => setTimeout(() => root.unmount())); // deferred: not during a render
          return;
        }
        onClickRef.current([e.lngLat.lng, e.lngLat.lat]);
      });

      // Report the visible area after every pan/zoom (moveend fires once per gesture, not per
      // frame), so the parent can subscribe to hazards in exactly this area.
      const reportViewport = () => {
        if (!map) return;
        const b = map.getBounds();
        onViewportRef.current([b.getWest(), b.getSouth(), b.getEast(), b.getNorth()]);
      };
      map.on("moveend", reportViewport);

      map.on("load", () => {
        if (!map) return;
        addRouteLayers(map);
        // Pointer cursor over hazard markers, so they read as clickable.
        map.on("mouseenter", "hazards", () => !reportingRef.current && (map!.getCanvas().style.cursor = "pointer"));
        map.on("mouseleave", "hazards", () => !reportingRef.current && (map!.getCanvas().style.cursor = ""));
        mapRef.current = map;
        setReady(true);
        reportViewport();
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
        // Same symbols as the From/To fields: a ring for the start, a filled pin for the end.
        marker = new MarkerClass({ element: markerElement(which), anchor: which === "start" ? "center" : "bottom" });
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

  // 5) Report mode: crosshair cursor while choosing a spot, and a marker on the chosen spot.
  useEffect(() => {
    const map = mapRef.current;
    const MarkerClass = markerClassRef.current;
    if (!ready || !map || !MarkerClass) return;
    map.getCanvas().style.cursor = reporting ? "crosshair" : "";
    if (reportPoint) {
      markersRef.current.report ??= new MarkerClass({ element: markerElement("report"), anchor: "bottom" });
      markersRef.current.report.setLngLat(reportPoint).addTo(map);
      // The report form opens over the lower part of the screen. Pan so the tapped spot sits
      // ~80 px below the top of the map, where the form can't cover it and the user can still
      // see what they are reporting. (offset moves the target point relative to the centre;
      // negative y = above it.)
      const h = map.getCanvas().clientHeight;
      map.easeTo({ center: reportPoint, offset: [0, -(h / 2 - Math.min(80, h * 0.2))], duration: 400 });
    } else {
      markersRef.current.report?.remove();
      markersRef.current.report = undefined;
    }
  }, [ready, reporting, reportPoint]);

  // 6) A place was picked from search: bring it into view (zooming in if the map is far out).
  useEffect(() => {
    const map = mapRef.current;
    if (!ready || !map || !focus) return;
    map.easeTo({ center: focus.point, zoom: Math.max(map.getZoom(), 16), duration: 600 });
  }, [ready, focus]);

  // 7) Live hazards -> circle markers, coloured by type; confirmed reports drawn larger.
  useEffect(() => {
    const map = mapRef.current;
    hazardsByIdRef.current = new Map(hazards.map((h) => [h.id, h]));
    if (!ready || !map) return;
    map.getSource<GeoJSONSource>("hazards")?.setData({
      type: "FeatureCollection",
      features: hazards.map((h) => ({
        type: "Feature",
        geometry: { type: "Point", coordinates: h.point },
        properties: { id: h.id, color: HAZARD_STYLE[h.type].color, confirmed: h.status === "confirmed" },
      })),
    });
  }, [ready, hazards]);

  return <div ref={containerRef} className="h-full w-full" role="application" aria-label="Map. Tap to set a start or destination, or tap a hazard for details." />;
}

/** Marker elements: a ring for the start, a pin for the destination, an amber pin for a report. */
function markerElement(kind: "start" | "end" | "report"): HTMLElement {
  const el = document.createElement("div");
  if (kind === "start") {
    el.style.cssText = "width:20px;height:20px;border-radius:9999px;background:#fff;border:5px solid #0f766e;box-shadow:0 1px 4px rgb(0 0 0/.35)";
    el.setAttribute("aria-label", "Start");
    return el;
  }
  const fill = kind === "end" ? "#0f766e" : "#d97706";
  el.innerHTML = `<svg width="30" height="38" viewBox="0 0 30 38" aria-hidden="true" style="filter:drop-shadow(0 2px 3px rgb(0 0 0/.3))"><path d="M15 37c0 0 13-12.6 13-22.5C28 6.9 22.2 1 15 1S2 6.9 2 14.5C2 24.4 15 37 15 37z" fill="${fill}" stroke="#fff" stroke-width="2"/><circle cx="15" cy="14.5" r="5" fill="#fff"/></svg>`;
  el.setAttribute("aria-label", kind === "end" ? "Destination" : "Problem location");
  return el;
}

// Register the empty sources and the layers that draw them. Later updates only call
// setData() on the sources; the layers' styling rules pick up the new features automatically.
function addRouteLayers(map: MapLibreMap) {
  const empty = { type: "FeatureCollection" as const, features: [] };
  map.addSource("route", { type: "geojson", data: empty });
  map.addSource("barriers", { type: "geojson", data: empty });
  map.addSource("snap-connectors", { type: "geojson", data: empty });
  map.addSource("hazards", { type: "geojson", data: empty });
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
    paint: { "line-color": "#78716c", "line-width": 1.5, "line-dasharray": [4, 3] },
  });
  // Dotted grey lines from each clicked point to where the route actually starts/ends.
  map.addLayer({
    id: "snap-connectors",
    type: "line",
    source: "snap-connectors",
    paint: { "line-color": "#57534e", "line-width": 3, "line-dasharray": [1, 1] },
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
  // Hazard markers go on top of the route so they stay clickable where they overlap it.
  map.addLayer({
    id: "hazards",
    type: "circle",
    source: "hazards",
    paint: {
      "circle-radius": ["case", ["get", "confirmed"], 10, 8],
      "circle-color": ["get", "color"],
      "circle-stroke-color": "#ffffff",
      "circle-stroke-width": 2.5,
    },
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
