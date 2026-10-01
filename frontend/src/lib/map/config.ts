import type { StyleSpecification } from 'maplibre-gl';

const TILE_URL =
	import.meta.env.VITE_TILE_URL ?? 'http://localhost:8082/data/{z}/{x}/{y}.pbf';

const API_URL = import.meta.env.VITE_API_URL ?? 'http://localhost:8080';
const WS_URL = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8081';

export { TILE_URL, API_URL, WS_URL };

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
			source: {
				id: 'composite',
				type: 'vector',
				url: TILE_URL,
				'layer-id': 'water'
			},
			paint: { 'fill-color': '#1e3a5f' }
		},
		{
			id: 'land',
			type: 'fill',
			source: {
				id: 'composite',
				type: 'vector',
				url: TILE_URL,
				'layer-id': 'landcover'
			},
			paint: { 'fill-color': '#1a2332' }
		},
		{
			id: 'roads',
			type: 'line',
			source: {
				id: 'composite',
				type: 'vector',
				url: TILE_URL,
				'layer-id': 'transportation'
			},
			paint: {
				'line-color': '#334155',
				'line-width': 1
			}
		},
		{
			id: 'labels',
			type: 'symbol',
			source: {
				id: 'composite',
				type: 'vector',
				url: TILE_URL,
				'layer-id': 'place'
			},
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