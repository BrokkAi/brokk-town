import { village } from "./scenery.js";
import { swarm } from "./swarm.js";

// A skin is presentation only. Every skin reads the same committed state and
// the same delivery events; it decides how a house, a working bot and a
// delivery look, and how long a delivery takes to cross the field.
export const skins = { village, swarm };
export const skinIds = Object.keys(skins);
export const skinHooks = [
  "landscape",
  "drawHouse",
  "drawWorking",
  "deliveryPoint",
  "drawDelivery",
];
export function normalizeSkin(value) {
  return skinIds.includes(value) ? value : skinIds[0];
}
export function nextSkin(value) {
  return skinIds[(skinIds.indexOf(normalizeSkin(value)) + 1) % skinIds.length];
}
