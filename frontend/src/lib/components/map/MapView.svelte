<script lang="ts">
	import maplibregl from 'maplibre-gl';
	import { mapStyle } from '$lib/map/config';
	import { selectedSignal, signals } from '$lib/stores/signals';
	import { tracks } from '$lib/stores/tracks';
	import { derived } from 'svelte/store';
	import { onMount } from 'svelte';

	let mapEl: HTMLDivElement;
	let map: maplibregl.Map;

	// Signal color by class
	const classColors: Record<string, string> = {
		aviation: '#3b82f6',
		land_mobile: '#22c55e',
		marine: '#06b6d4',
		broadcast: '#f59e0b',
		amateur: '#a855f7',
		gnss: '#ef4444',
		wifi: '#6366f1',
		unknown: '#64748b'
	};

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

		// Add signal markers layer
		map.on('load', () => {
			map.addSource('signals', {
				type: 'geojson',
				data: { type: 'FeatureCollection', features: [] }
			});

			// Accuracy circles
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

			// Signal dots
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

			// Labels
			map.addLayer({
				id: 'signal-labels',
				type: 'symbol',
				source: 'signals',
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

			// Selected signal's track (§9.4)
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
		});

		// Update markers when signals change
		const unsub = signals.subscribe(($signals) => {
			const features = $signals.flatMap((s) => {
				// A1 (§9.3): unlocated signals (null lat/lon) are omitted from the map
				if (s.lat == null || s.lon == null) return [];
				return [{
					type: 'Feature' as const,
					geometry: { type: 'Point' as const, coordinates: [s.lon, s.lat] },
					properties: {
						id: s.id,
						color: classColors[s.class] ?? '#64748b',
						accuracy: s.accuracyM || 1000,
						label: `${(s.freqHz / 1e6).toFixed(1)} MHz`
					}
				}];
			});

			const source = map.getSource('signals') as maplibregl.GeoJSONSource;
			if (source) {
				source.setData({ type: 'FeatureCollection', features });
			}
		});

		// Selected signal's track polyline (§9.4): drawn once a REST
		// fetch has delivered a path with two or more fixes.
		const trackData = derived([selectedSignal, tracks], ([$sel, $t]) => {
			const empty = { type: 'FeatureCollection' as const, features: [] as unknown[] };
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
					}
				]
			};
		});
		const unsubTrack = trackData.subscribe((data) => {
			const source = map.getSource('signal-track') as maplibregl.GeoJSONSource | undefined;
			source?.setData(data as Parameters<maplibregl.GeoJSONSource['setData']>[0]);
		});

		return () => {
			unsub();
			unsubTrack();
			map.remove();
		};
	});
</script>

<div bind:this={mapEl} class="w-full h-full"></div>