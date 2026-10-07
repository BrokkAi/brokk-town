const $ = (id) => document.querySelector(`#${id}`);
const busy = (status) => ["queued", "gathering", "answering"].includes(status);
const pendingKey = "slopcop-squad-guide-pending";

// These handlers are the only network entry points. render only consumes
// state; reconnecting SSE never resubmits or confirms a conversation.
export function guidePanel({api, getTown, getState, onOpen = () => {}}) {
  let townID = "", sending = false, pending = null, order = "";
  const rows = new Map(), conversations = new Map();
  try { pending = JSON.parse(sessionStorage.getItem(pendingKey) || "null"); } catch {}
  function savePending(value) { pending = value; try { value ? sessionStorage.setItem(pendingKey, JSON.stringify(value)) : sessionStorage.removeItem(pendingKey); } catch {} }
  function acknowledge(id, origin) {
    if (pending?.id !== id || pending?.town !== origin) return;
    const submitted = pending; savePending(null);
    if (origin === townID && $("guide-question").value.trim() === submitted.question) $("guide-question").value = "";
  }
  function selected() { return getState()?.towns?.[townID]; }
  async function command(body) {
    if (sending) return;
    sending = true; $("guide-error").textContent = ""; render();
    const origin = townID;
    try {
      const conversation = await api("/api/guide", {town: origin, ...body}, AbortSignal.timeout(30000));
      conversations.set(origin, conversation);
      if (origin === townID) renderConversation(conversation);
      if (body.action === "ask") acknowledge(body.id, origin);
    } catch (error) {
      const rejected = error.status >= 400 && error.status < 500 && error.status !== 408;
      if (rejected && body.action === "ask" && pending?.id === body.id) savePending(null);
      if (origin === townID) $("guide-error").textContent = rejected ? error.message : `${error.message} Check the conversation before trying again; the submission may have been saved.`;
    } finally { sending = false; render(); }
  }
  function row(turn) {
    if (rows.has(turn.id)) return rows.get(turn.id);
    const article = document.createElement("article"), question = document.createElement("p"), answer = document.createElement("p"), status = document.createElement("p"), proposal = document.createElement("p"), confirm = document.createElement("button");
    article.className = "guide-turn"; question.className = "guide-question"; answer.className = "guide-answer"; status.className = "muted";
    confirm.type = "button"; confirm.textContent = "Confirm this pause";
    article.append(question, answer, status, proposal, confirm);
    const item = {article, question, answer, status, proposal, confirm}; rows.set(turn.id, item); return item;
  }
  function renderConversation(conversation = {next:0, turns:[]}) {
    const cached = conversations.get(townID);
    if (cached && (cached.revision || 0) > (conversation.revision || 0)) conversation = cached;
    else conversations.set(townID, conversation);
    const turns = conversation.turns || [];
    if (pending?.town === townID && turns.some((turn) => turn.id === pending.id)) acknowledge(pending.id, townID);
    const ids = turns.map((turn) => turn.id).join(",");
    for (const turn of turns) {
      const item = row(turn);
      item.question.textContent = `You: ${turn.question}`; item.answer.textContent = `Sergeant: ${turn.answer || (busy(turn.status) ? "…" : "No completed answer.")}`;
      item.status.textContent = `${turn.status}${turn.detail ? ` · ${turn.detail}` : ""}`;
      item.proposal.textContent = turn.proposal ? `${turn.proposal.status === "confirmed" ? "Confirmed" : "Proposed action"}: ${turn.proposal.description}` : "";
      item.confirm.hidden = !turn.proposal || turn.proposal.status === "confirmed";
      item.confirm.disabled = sending;
      item.confirm.onclick = () => command({action:"confirm", id:turn.id, digest:turn.proposal.digest});
    }
    if (order !== ids) {
      $("guide-turns").replaceChildren(...turns.map((turn) => row(turn).article)); order = ids;
      for (const id of rows.keys()) if (!turns.some((turn) => turn.id === id)) rows.delete(id);
    }
    const active = turns.find((turn) => busy(turn.status));
    $("guide-status").textContent = active ? `The Desk Sergeant is ${active.status}.` : "The Desk Sergeant is ready. Answers describe saved observations.";
    $("guide-send").disabled = sending || !!active || !selected();
    $("guide-cancel").disabled = sending || !active;
    $("guide-cancel").onclick = () => active && command({action:"cancel",id:active.id});
    $("guide-retry").hidden = !(pending?.town === townID);
    $("guide-retry").disabled = sending;
  }
  function render() {
    $("ask-guide").disabled = !getTown();
    if (!$("guide-dialog").open) return;
    const town = selected();
    $("guide-town").textContent = townID;
    const profile = town?.config;
    $("guide-profile").textContent = profile ? `Uses precinct defaults: ${profile.harness || "configured harness"} · ${profile.model || "default model"} · ${profile.effort || "default effort"}.` : "This town is no longer available.";
    renderConversation(town?.guide);
  }
  $("guide-form").onsubmit = (event) => {
    event.preventDefault();
    const question = $("guide-question").value.trim(), town = selected();
    if (!question || !town || sending) return;
    if (pending?.town === townID) { $("guide-error").textContent = "Resolve the saved submission with Retry before asking a new question."; return; }
    const body = {town:townID,action:"ask",id:crypto.randomUUID(),sequence:conversations.get(townID)?.next ?? town.guide?.next ?? 0,question};
    savePending(body); void command(body);
  };
  $("guide-retry").onclick = () => { if (pending?.town === townID) return command(pending); };
  $("guide-refresh").onclick = () => command({action:"read"});
  $("guide-close").onclick = () => $("guide-dialog").close();
  $("ask-guide").onclick = () => {
    const town = getTown(); if (!town) return;
    if (townID !== town.id) { rows.clear(); order = ""; $("guide-turns").replaceChildren(); $("guide-question").value = ""; }
    townID = town.id;
    onOpen(); $("guide-dialog").showModal(); render(); $("guide-question").focus();
  };
  return {render};
}

