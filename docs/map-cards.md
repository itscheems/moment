# Map cards

Posts and articles use the same interactive map card. The post composer stores one card alongside Markdown, media and the existing location label. Articles store any number of inline map blocks through Tiptap's React node views. Both surfaces share place selection, map controls, expansion and globe view.

Authors can search worldwide, use their current location, or pick a point on the map. A card holds 1–20 ordered places. It displays markers by default; enabling **连接地点** joins them in list order. Authors hold a place row to reorder it, or use Space and the arrow keys. Right-clicking a row or swiping it left exposes deletion with confirmation; cancelling restores the row. Each place also opens in Google Maps. A map can be the only content of an ordinary post.

Expansion fills the browser viewport. Close and globe controls sit on the left; the place list floats at the upper right and animates into a small round toggle when collapsed. Selecting a place uses MapLibre's native `flyTo` camera animation, while its external link continues to open Google Maps. Fullscreen maps accept ordinary wheel and touch gestures; embedded maps retain cooperative gestures for scrolling the surrounding page.

Editing surfaces add a close control beside the card's expand control to remove the map. Reader cards expose expansion alone. Revealed swipe actions have rounded corners and animate closed when pressing outside their row. Pointer selection uses Motion's drag-aware press lifecycle so a swipe or reorder does not also select a place.

## Services

The browser renders OpenFreeMap's Liberty vector tiles through MapLibre GL JS. Rendering requires no map API key. Search runs through the backend's existing AMap and Geoapify integrations. Set `AMAP_API_KEY` and `GEOAPIFY_API_KEY` in the server process for domestic and worldwide search. Keys stay on the server. Global search queries the configured providers concurrently without restricting results to the device's country.

The server needs network access to `restapi.amap.com` and `api.geoapify.com`; browsers need access to `tiles.openfreemap.org`. The Go HTTP client respects the usual `HTTPS_PROXY` and `NO_PROXY` environment variables. A proxy configured only for a desktop browser does not configure the server process. Without search credentials, authors can still select and name points on the map.

The renderer preserves OpenFreeMap, OpenMapTiles and OpenStreetMap attribution. Its worker is bundled locally. The application's content security policy permits the tile host explicitly and keeps workers on the same origin.

## Loading and data

The engine is a separate lazy chunk. Cards prefetch it near the viewport, create a map when visible, and release the instance after three seconds outside the viewport. Returning cards restore their camera. Expansion transfers the live view to the dialog instead of retaining two canvases. Readers can switch between a flat map and a globe in the expanded view. Failed map loads retain the place list and external links. Sorting loads separately for authors; the reader entry excludes its runtime.

Initial framing fits all places, including nearby stops at street level. Post share images retain the current map centre, zoom and aspect ratio. Camera adjustments belong to the current page only; refreshing fits the places again without changing the saved card.

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
| Base UI | 1.6.0 | Dialog focus, dismissal, context menus and switches |
| dnd-kit core / sortable / modifiers / utilities | 6.3.1 / 10.0.0 / 9.0.0 / 3.2.2 | Mouse, touch and keyboard sorting |
| Motion | 13.1.0 | Horizontal swipe, spring return and row transitions |

Moment owns the portable snapshot, place picker, localization, viewport lifecycle and editor adapters. Sorting and swipe transforms use separate elements. Dialog configuration lets sorting arrow keys reach dnd-kit, while the context menu keeps touch long-press reserved for sorting.
