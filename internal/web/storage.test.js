import test, { beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { storagePanel } from "./storage.js";
let fields;
function node() { return { children: [], dataset: {}, value: "168", listeners: {}, append(child) { this.children.push(child); }, replaceChildren(...children) { this.children = children; }, setAttribute() {}, querySelectorAll() { return this.children.map((row) => row.children[0].children[0]); }, addEventListener(name, fn) { this.listeners[name] = fn; }, showModal() {}, close() { this.listeners.close(); } }; }
beforeEach(() => { fields = Object.fromEntries(["storage-refresh", "storage-age", "storage-remove", "storage-items", "storage-summary", "storage-hold", "storage-error", "storage-status", "storage-close", "storage-dialog", "storage-town", "town-storage"].map((id) => [id, node()])); globalThis.document = { querySelector: (id) => fields[id.slice(1)], createElement: node }; });
afterEach(() => { delete globalThis.document; });
const tick = () => new Promise((resolve) => setImmediate(resolve));
const inventory = { roles: {issue:{bytes:12,files:2,artifacts:2}}, minimum_age_hours:168, artifacts:[{id:"safe",path:"safe.jsonl",bytes:6,modified:"2026-09-01T00:00:00Z",eligible:true,reason:"Completed"},{id:"keep",path:"<private>",bytes:6,modified:"2026-09-01T00:00:00Z",eligible:false,reason:"Uncertain"}] };
test("storage only inspects after opening and removes explicitly selected eligible IDs", async () => {
 const calls=[];storagePanel({getTown:()=>({id:"acme/project"}),api:async(url,body)=>{calls.push({url,body});return url.endsWith("cleanup")?[{id:"safe",status:"removed",detail:"Done"}]:inventory;}});
 assert.equal(calls.length,0);
 fields["town-storage"].onclick();await tick();
 assert.equal(calls[0].url,"/api/storage");
 const boxes=fields["storage-items"].querySelectorAll("input");assert.equal(boxes[1].disabled,true);
 boxes[0].checked=true;boxes[0].onchange();assert.equal(fields["storage-remove"].disabled,false);
 await fields["storage-remove"].onclick();
 assert.deepEqual(calls[1],{url:"/api/storage/cleanup",body:{town:"acme/project",minimum_age_hours:168,ids:["safe"]}});
 assert.equal(fields["storage-remove"].disabled,true);assert.match(fields["storage-status"].textContent,/removed/);
});
test("storage rejects changed retention and cancels stale responses and duplicate scans", async () => {
 let finish,signal,calls=0;
 storagePanel({getTown:()=>({id:"acme/project"}),api:async(_url,_body,s)=>{calls++;signal=s;return new Promise((resolve)=>{finish=resolve;});}});
 fields["town-storage"].onclick();await fields["storage-refresh"].onclick();assert.equal(calls,1);
 fields["storage-dialog"].close();assert.equal(signal.aborted,true);finish(inventory);await tick();assert.equal(fields["storage-items"].children.length,0);
});
test("failed cleanup requires refreshed inventory and never retries automatically", async () => {
 let calls=0;storagePanel({getTown:()=>({id:"acme/project"}),api:async(url)=>{calls++;if(url.endsWith("cleanup"))throw new Error("Lost receipt");return inventory;}});
 fields["town-storage"].onclick();await tick();
 const box=fields["storage-items"].querySelectorAll("input")[0];box.checked=true;box.onchange();
 fields["storage-age"].value="0";await fields["storage-remove"].onclick();assert.equal(calls,1);
 fields["storage-age"].value="168";await fields["storage-remove"].onclick();assert.equal(calls,2);assert.equal(fields["storage-remove"].disabled,true);assert.match(fields["storage-error"].textContent,/Lost receipt/);
});
