# Map cards

Posts and articles use the same interactive map card. The post composer stores one card alongside Markdown, media and the existing location label. Articles store any number of inline map blocks through Tiptap's React node views. Both surfaces share place selection, map controls, expansion and globe view.

Authors can search worldwide, use their current location, or pick a point on the map. A card holds 1–20 ordered places. It displays markers by default; enabling **连接地点** joins them in list order. The list supports adding, moving and removing places. Each place also opens in Google Maps. A map can be the only content of an ordinary post.

## Services

The browser renders OpenFreeMap's Liberty vector tiles through MapLibre GL JS. Rendering requires no map API key. Search runs through the backend's existing AMap and Geoapify integrations. Set `AMAP_API_KEY` and `GEOAPIFY_API_KEY` in the server process for domestic and worldwide search. Keys stay on the server. Global search queries the configured providers concurrently without restricting results to the device's country.

The server needs network access to `restapi.amap.com` and `api.geoapify.com`; browsers need access to `tiles.openfreemap.org`. The Go HTTP client respects the usual `HTTPS_PROXY` and `NO_PROXY` environment variables. A proxy configured only for a desktop browser does not configure the server process. Without search credentials, authors can still select and name points on the map.

The renderer preserves OpenFreeMap, OpenMapTiles and OpenStreetMap attribution. Its worker is bundled locally. The application's content security policy permits the tile host explicitly and keeps workers on the same origin.

## Loading and data

The engine is a separate lazy chunk. Cards prefetch it near the viewport, create a map when visible, and release the instance after three seconds outside the viewport. Returning cards restore their camera. Expansion transfers the live view to the dialog instead of retaining two canvases. Readers can switch between a flat map and a globe in the expanded view. Failed map loads retain the place list and external links.

Ordinary cards are persisted with their entries; article cards are embedded value snapshots. Reading requires no geocoding request. WGS84 and GCJ-02 source coordinates round-trip unchanged; only the renderer converts them for the basemap and external links. Map labels participate in search and summaries. Author, draft and visibility rules follow the surrounding content.

Article schema 3 adds inline maps while schemas 1 and 2 remain readable. Full-site export format 3 preserves ordinary cards, article maps and drafts. Import reads formats 2 and 3, and rejects maps disguised as format 2. See the [API contract](client-api.md#published-map-cards) and [archive contract](export-format-v2.md).

GPS file import, recorded tracks, navigation and building models are outside this version.

## Upstream versions and integration

| Dependency | Locked version | Responsibility |
| --- | --- | --- |
| MapLibre GL JS | 6.15.0 | Map, markers, native controls, planar/globe projection |
| react-map-gl | 8.1.3 | React lifecycle for MapLibre |
| OpenFreeMap | Liberty service style | Vector basemap and labels |
| Turf great-circle | 7.4.0 | Ordered point connections across the date line |
| gcoord | 1.0.7 | GCJ-02 to WGS84 display conversion |
| Tiptap | 3.31.3 | Article node persistence and upstream React node-view lifecycle |
| Base UI | 1.6.0 | Dialog focus, dismissal and keyboard behavior |

Moment owns the portable snapshot, place picker, localization, viewport lifecycle and editor adapters. It uses upstream map and editor interaction lifecycles rather than adding a second pointer or selection system.
