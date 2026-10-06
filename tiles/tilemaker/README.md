# Vendored tilemaker resources

`tilemaker-config.json` and `process-openmaptiles.lua` come from
tilemaker v3.2.0 — upstream files `resources/config-openmaptiles.json`
and `resources/process-openmaptiles.lua` at
<https://github.com/systemed/tilemaker> — with one change: the four
shapefile-backed layers (`ocean`, `urban_areas`, `ice_shelf`,
`glacier`) are removed so a render needs nothing but the `.osm.pbf`.
Upstream pairs those layers with the ~2 GB coastline/landcover packs
from `get-coastline.sh`/`get-landcover.sh`; restore them per
tilemaker's RUNNING.md if you want sea tiles.

The layers follow the OpenMapTiles schema the workbench map style
draws (`water`, `landcover`, `transportation`, `place` in
`frontend/src/lib/map/config.ts`). Zoom range lives in
`settings.minzoom`/`settings.maxzoom` (shipped 0–14);
`setup-tiles.sh` patches a temporary copy when you pass explicit
zoom arguments.

There is no Homebrew formula for tilemaker (`brew install tilemaker`
fails: never in core, and the old systemed/tilemaker tap was removed).
Native install, per upstream `docs/INSTALL.md`:

    brew install boost lua shapelib rapidjson
    git clone https://github.com/systemed/tilemaker
    cd tilemaker && make && sudo make install

`tiles/setup-tiles.sh` otherwise falls back to the project Docker
image (ghcr.io/systemed/tilemaker) with no local install at all.
