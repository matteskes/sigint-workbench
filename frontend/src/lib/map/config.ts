import type { StyleSpecification } from 'maplibre-gl';

const TILE_URL =
	import.meta.env.VITE_TILE_URL ?? 'http://localhost:8082/data/v3/{z}/{x}/{y}.pbf';

export { TILE_URL };

// Dark basemap style for the MapLibre map
export const mapStyle: StyleSpecification = {
	version: 8,
	sources: {
		osm: {
			type: 'vector',
			url: TILE_URL
		}
	},
	layers: [
		{
			id: 'background',
			type: 'background',
			paint: { 'background-color': '#0f172a' }
		},
		{
			id: 'water',
			type: 'fill',
			source: 'osm',
			'source-layer': 'water',
			paint: { 'fill-color': '#1e3a5f' }
		},
		{
			id: 'land',
			type: 'fill',
			source: 'osm',
			'source-layer': 'landcover',
			paint: { 'fill-color': '#1a2332' }
		},
		{
			id: 'roads',
			type: 'line',
			source: 'osm',
			'source-layer': 'transportation',
			paint: {
				'line-color': '#334155',
				'line-width': 1
			}
		},
		{
			id: 'labels',
			type: 'symbol',
			source: 'osm',
			'source-layer': 'place',
			layout: {
				'text-field': ['get', 'name'],
				'text-size': 12
			},
			paint: {
				'text-color': '#94a3b8'
			}
		}
	]
};