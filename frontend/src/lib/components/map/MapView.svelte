<script lang="ts">
	// §5 Operations map. Carries over the signal dots + accuracy halos +
	// track polyline, and adds: the §12.1 receiver layer (solid dot +
	// green ring = active, hollow = idle, sky halo = sweeping, §7.4 —
	// sweep state from the shared sdrRuntime cache), layer toggles
	// (state in the ui store), selected-only labels (declutter),
	// click-to-select for signals and receivers, and the Inspector's
	// "center map" command (reduced-motion aware). The class palette
	// comes from classColor.ts — the duplicate map that used to live
	// here is gone (§4).
	import maplibregl from 'maplibre-gl';
	import { mapStyle } from '$lib/map/config';
	import { signals, selectedSignal, type Signal } from '$lib/stores/signals';
	import { tracks } from '$lib/stores/tracks';
import { tdoaResults } from '$lib/stores/tdoa';
	import { sdrs, sdrRuntime } from '$lib/stores/sdrs';
	import { layers, mapReceiverId, mapCenterRequest, type LayerToggles } from '$lib/stores/ui';
	import { classHex } from '$lib/ui/classColor';
	import { get, derived } from 'svelte/store';
	import { onMount } from 'svelte';

	let mapEl: HTMLDivElement;
	let map: maplibregl.Map;

	// §16: honour reduced motion — no animated fly-to.
	function prefersReducedMotion(): boolean {
		return (
			typeof matchMedia === 'function' &&
			matchMedia('(prefers-reduced-motion: reduce)').matches
		);
	}

	function point(id: string, lon: number, lat: number, props: Record<string, unknown>): GeoJSON.Feature {
		return {
			type: 'Feature',
			geometry: { type: 'Point', coordinates: [lon, lat] },
			properties: { id, ...props }
		};
	}

	onMount(() => {
		map = new maplibregl.Map({
			container: mapEl,
			style: mapStyle as any,
			center: [-122.4194, 37.7749], // San Francisco default
			zoom: 10,
			attributionControl: false
		});

		map.addControl(new maplibregl.NavigationControl(), 'top-right');
		map.addControl(new maplibregl.ScaleControl(), 'bottom-left');

		map.on('load', () => {
			// ── signals: dots + accuracy halos (labels are separate) ──
			map.addSource('signals', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});
			map.addLayer({
				id: 'signal-accuracy',
				type: 'circle',
				source: 'signals',
				paint: {
					'circle-radius': ['interpolate', ['linear'], ['get', 'accuracy'], 0, 50, 10000, 500],
					'circle-color': ['get', 'color'],
					'circle-opacity': 0.15,
					'circle-stroke-width': 1,
					'circle-stroke-color': ['get', 'color'],
					'circle-stroke-opacity': 0.3
				}
			});
			map.addLayer({
				id: 'signal-dots',
				type: 'circle',
				source: 'signals',
				paint: {
					'circle-radius': 6,
					'circle-color': ['get', 'color'],
					'circle-stroke-width': 2,
					'circle-stroke-color': '#fff'
				}
			});

			// ── selected-signal label: one feature, so 500 signals never
			// become 500 labels (§5 declutter) ──
			map.addSource('signal-labels', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});
			map.addLayer({
				id: 'signal-labels',
				type: 'symbol',
				source: 'signal-labels',
				layout: {
					'text-field': ['get', 'label'],
					'text-size': 11,
					'text-offset': [0, 1.2]
				},
				paint: {
					'text-color': '#e2e8f0',
					'text-halo-color': '#0f172a',
					'text-halo-width': 1
				}
			});

			// ── receivers (§12.1): active = solid dot; idle = hollow;
			// sweeping = outer sky halo (static — a rotating dash would
			// repaint the map every frame for zero information) ──
			map.addSource('receivers', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});
			map.addLayer({
				id: 'receiver-sweep',
				type: 'circle',
				source: 'receivers',
				filter: ['==', ['get', 'sweeping'], true],
				paint: {
					'circle-radius': 12,
					'circle-color': '#00000000',
					'circle-stroke-color': '#38bdf8',
					'circle-stroke-width': 2,
					'circle-stroke-opacity': 0.9
				}
			});
			map.addLayer({
				id: 'receiver-dots',
				type: 'circle',
				source: 'receivers',
				paint: {
					'circle-radius': 5,
					'circle-color': ['case', ['get', 'active'], '#22c55e', '#0f172a'],
					'circle-stroke-width': 1.5,
					'circle-stroke-color': '#e2e8f0'
				}
			});

			// ── selected signal's persisted track (§9.4) ──
			map.addSource('signal-track', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});
			map.addLayer({
				id: 'signal-track-line',
				type: 'line',
				source: 'signal-track',
				layout: { 'line-cap': 'round', 'line-join': 'round' },
				paint: {
					'line-color': '#38bdf8',
					'line-width': 3,
					'line-opacity': 0.9
				}
			});

			// ── selected signal's TDOA locus (§9.6): the hyperbolic arc
			// between the two locus endpoints when a solve was inconclusive ──
			map.addSource('tdoa-locus', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});
			map.addLayer({
				id: 'tdoa-locus-line',
				type: 'line',
				source: 'tdoa-locus',
				layout: { 'line-cap': 'round' },
				paint: {
					'line-color': '#a78bfa',
					'line-width': 2,
					'line-dasharray': [2, 2],
					'line-opacity': 0.9
				}
			});

			applyVisibility(get(layers));

			// F1: map dot click selects (row click / map dot / ?signal= —
			// all coherent). Selection alone never moves the viewport.
			map.on('click', 'signal-dots', (e) => {
				const id = e.features?.[0]?.properties?.id as string | undefined;
				if (!id) return;
				const sig = get(signals).find((s) => s.id === id);
				if (sig) selectedSignal.set(sig);
			});
			map.on('click', 'receiver-dots', (e) => {
				const id = e.features?.[0]?.properties?.id as string | undefined;
				if (id) mapReceiverId.set(id);
			});
			for (const layer of ['signal-dots', 'receiver-dots']) {
				map.on('mouseenter', layer, () => (map.getCanvas().style.cursor = 'pointer'));
				map.on('mouseleave', layer, () => (map.getCanvas().style.cursor = ''));
			}
		});

		function applyVisibility($l: LayerToggles): void {
			if (!map.getSource('signals')) return;
			const groups: Record<string, string[]> = {
				signals: ['signal-accuracy', 'signal-dots'],
				labels: ['signal-labels'],
				tracks: ['signal-track-line', 'tdoa-locus-line'],
				receivers: ['receiver-dots', 'receiver-sweep']
			};
			for (const [key, names] of Object.entries(groups) as [keyof LayerToggles, string[]][]) {
				for (const name of names) {
					if (map.getLayer(name)) {
						map.setLayoutProperty(name, 'visibility', $l[key] ? 'visible' : 'none');
					}
				}
			}
		}

		// ── SUBSCRIPTIONS ──
		// Signals: rebuilt on coalesced flush boundaries only (§14.3).
		const unsubSignals = signals.subscribe(($signals) => {
			const features: GeoJSON.Feature[] = [];
			// A1 (§9.3): unlocated signals are omitted from the map — the
			// table's unlocated chip is their honest representation.
			for (const s of $signals) {
				if (s.lat == null || s.lon == null) continue;
				features.push(
					point(s.id, s.lon, s.lat, {
						color: classHex(s.class),
						accuracy: s.accuracyM || 1000
					})
				);
			}
			const source = map.getSource('signals') as maplibregl.GeoJSONSource | undefined;
			source?.setData({ type: 'FeatureCollection', features });
		});

		// Selected-signal label (single feature).
		const labelData = derived(selectedSignal, ($sel) => {
			const empty = { type: 'FeatureCollection' as const, features: [] as GeoJSON.Feature[] };
			if (!$sel || $sel.lat == null || $sel.lon == null) return empty;
			return {
				type: 'FeatureCollection' as const,
				features: [
					point($sel.id, $sel.lon, $sel.lat, {
						label: `${($sel.freqHz / 1e6).toFixed(3)} MHz`
					})
				]
			};
		});
		const unsubLabels = labelData.subscribe((data) => {
			const source = map.getSource('signal-labels') as maplibregl.GeoJSONSource | undefined;
			source?.setData(data as Parameters<maplibregl.GeoJSONSource['setData']>[0]);
		});

		// Selected signal's track polyline: drawn once a REST fetch has
		// delivered a path with two or more fixes (§9.4).
		const trackData = derived([selectedSignal, tracks], ([$sel, $t]) => {
			const empty = { type: 'FeatureCollection' as const, features: [] as GeoJSON.Feature[] };
			if (!$sel) return empty;
			const tr = $t[$sel.id];
			if (!tr || tr.path.length < 2) return empty;
			return {
				type: 'FeatureCollection' as const,
				features: [
					{
						type: 'Feature' as const,
						geometry: {
							type: 'LineString' as const,
							coordinates: tr.path.map((p) => [p.lon, p.lat])
						},
						properties: {}
					} as GeoJSON.Feature
				]
			};
		});
		const unsubTrack = trackData.subscribe((data) => {
			const source = map.getSource('signal-track') as maplibregl.GeoJSONSource | undefined;
			source?.setData(data as Parameters<maplibregl.GeoJSONSource['setData']>[0]);
		});

		// Selected signal's TDOA locus (§9.6): dashed violet segment
		// between the two locus endpoints of the latest signal.tdoa
		// attempt — shown while no unique fix exists (an accepted fix
		// moves the signal dot instead). Rides the Tracks layer toggle:
		// the same per-signal geometry overlay.
		const locusData = derived([selectedSignal, tdoaResults], ([$sel, $t]) => {
			const empty = { type: 'FeatureCollection' as const, features: [] as GeoJSON.Feature[] };
			if (!$sel) return empty;
			const loc = $t[$sel.id]?.locus;
			if (!loc) return empty;
			return {
				type: 'FeatureCollection' as const,
				features: [
					{
						type: 'Feature' as const,
						geometry: {
							type: 'LineString' as const,
							coordinates: [
								[loc.lng1, loc.lat1],
								[loc.lng2, loc.lat2]
							]
						},
						properties: {}
					} as GeoJSON.Feature
				]
			};
		});
		const unsubLocus = locusData.subscribe((data) => {
			const source = map.getSource('tdoa-locus') as maplibregl.GeoJSONSource | undefined;
			source?.setData(data as Parameters<maplibregl.GeoJSONSource['setData']>[0]);
		});

		// Receivers: plotted only when the device row carries a position
		// (0/0 rows are "unplaced" — never invented onto the map).
		const receiverData = derived([sdrs, sdrRuntime], ([$sdrs, $rt]) => {
			const features: GeoJSON.Feature[] = [];
			for (const d of $sdrs) {
				if (d.lat == null || d.lon == null) continue;
				const rt = $rt[d.id];
				features.push(
					point(d.id, d.lon, d.lat, {
						active: d.active,
						sweeping: Boolean(rt?.scanning && !rt?.scanPaused)
					})
				);
			}
			return { type: 'FeatureCollection' as const, features };
		});
		const unsubReceivers = receiverData.subscribe((data) => {
			const source = map.getSource('receivers') as maplibregl.GeoJSONSource | undefined;
			source?.setData(data as Parameters<maplibregl.GeoJSONSource['setData']>[0]);
		});

		// Layer toggles (§5 bottom-left popover, state in the ui store).
		const unsubLayers = layers.subscribe(($l) => applyVisibility($l));

		// Inspector "center map" command — explicit action only (F1).
		const unsubCenter = mapCenterRequest.subscribe((req) => {
			if (!req || !map) return;
			const target: maplibregl.LngLatLike = [req.lon, req.lat];
			if (prefersReducedMotion()) map.jumpTo({ center: target, zoom: Math.max(map.getZoom(), 13) });
			else map.easeTo({ center: target, zoom: Math.max(map.getZoom(), 13), duration: 500 });
		});

		return () => {
			unsubSignals();
			unsubLabels();
			unsubTrack();
			unsubLocus();
			unsubReceivers();
			unsubLayers();
			unsubCenter();
			map.remove();
		};
	});
</script>

<div bind:this={mapEl} class="h-full w-full" role="application" aria-label="Signal map"></div>
