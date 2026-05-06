(function () {
  "use strict";

  const comments = [];

  const doc = document.getElementById("pr-doc");
  const popup = document.getElementById("pr-popup");
  const popupAnchorText = document.getElementById("pr-popup-anchor-text");
  const popupBody = document.getElementById("pr-popup-body");
  const popupSave = document.getElementById("pr-popup-save");
  const popupCancel = document.getElementById("pr-popup-cancel");
  const popupDelete = document.getElementById("pr-popup-delete");
  const countEl = document.getElementById("pr-count");
  const approveBtn = document.getElementById("pr-approve");
  const feedbackBtn = document.getElementById("pr-feedback");

  // popupState holds the work-in-progress: either a brand-new selection
  // waiting to be saved, or an existing comment being edited.
  //   new:  { mode: "new",  range, anchorText, lineStart, lineEnd }
  //   edit: { mode: "edit", id }
  let popupState = null;

  function makeCommentId() {
    if (window.crypto && typeof window.crypto.randomUUID === "function") {
      return window.crypto.randomUUID();
    }
    return "c-" + Math.random().toString(36).slice(2) + Date.now().toString(36);
  }

  function findCommentById(id) {
    return comments.find((c) => c.id === id) || null;
  }

  function findAnchoredSpan(id) {
    // CSS.escape guards against any non-alnum chars that might leak into
    // id producers in the future. Current producers (randomUUID and the
    // base36 fallback) stay inside [a-z0-9-], but don't couple this
    // selector to that invariant.
    const safe = typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id;
    return doc.querySelector('.pr-anchored[data-comment-id="' + safe + '"]');
  }

  // ----- text selection → popup -----
  //
  // We track mousedown to distinguish a click-on-highlight (open edit) from
  // a drag that happens to start inside a highlight (start a new selection,
  // existing behavior). A move of more than a few pixels between mousedown
  // and mouseup counts as a drag.

  const CLICK_SLOP_PX = 4;
  let mouseDownAt = null;

  doc.addEventListener("mousedown", function (e) {
    mouseDownAt = { x: e.clientX, y: e.clientY, target: e.target };
  });

  doc.addEventListener("mouseup", function (e) {
    const down = mouseDownAt;
    mouseDownAt = null;
    // Defer one tick so the selection object is settled.
    setTimeout(() => handleMouseUp(e, down), 0);
  });

  function handleMouseUp(e, down) {
    // If the user didn't move much and ended on an existing highlight,
    // treat it as a click-to-edit.
    if (down) {
      const dx = Math.abs(e.clientX - down.x);
      const dy = Math.abs(e.clientY - down.y);
      if (dx <= CLICK_SLOP_PX && dy <= CLICK_SLOP_PX) {
        const span = findAnchoredAncestor(e.target);
        if (span) {
          // Clear any accidental caret-click selection so the popup doesn't
          // also try to treat this as a new-selection event.
          const sel = window.getSelection();
          if (sel) sel.removeAllRanges();
          openEditPopup(span);
          return;
        }
      }
    }
    handleNewSelection();
  }

  function findAnchoredAncestor(node) {
    while (node && node !== doc) {
      if (
        node.nodeType === 1 &&
        node.classList &&
        node.classList.contains("pr-anchored")
      ) {
        return node;
      }
      node = node.parentNode;
    }
    return null;
  }

  function handleNewSelection() {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return;

    const range = sel.getRangeAt(0);
    const text = sel.toString().trim();
    if (!text) return;

    // Confirm selection is inside the doc, not sidebar/footer.
    if (!doc.contains(range.commonAncestorContainer)) return;

    // Find the pr-block bounds containing the selection for line mapping.
    const startBlock = findBlock(range.startContainer);
    const endBlock = findBlock(range.endContainer);
    if (!startBlock) return;

    const lineStart = parseInt(startBlock.dataset.lineStart, 10);
    const lineEnd = parseInt(
      (endBlock || startBlock).dataset.lineEnd,
      10
    );

    popupState = {
      mode: "new",
      range: range.cloneRange(),
      anchorText: text,
      lineStart,
      lineEnd,
    };

    const rect = range.getBoundingClientRect();
    showPopup(rect, text, "");
    popupDelete.hidden = true;
  }

  function findBlock(node) {
    while (node && node !== doc) {
      if (node.nodeType === 1 && node.classList && node.classList.contains("pr-block")) {
        return node;
      }
      node = node.parentNode;
    }
    return null;
  }

  function showPopup(rect, anchorText, bodyValue) {
    popup.hidden = false;
    popupAnchorText.textContent =
      anchorText.length > 60 ? anchorText.slice(0, 60) + "…" : anchorText;
    popupBody.value = bodyValue || "";

    // Render once hidden=false so offsetWidth is real.
    const top = window.scrollY + rect.bottom + 6;
    const left = Math.min(
      window.scrollX + rect.left,
      window.innerWidth - popup.offsetWidth - 20
    );
    popup.style.top = top + "px";
    popup.style.left = Math.max(left, 10) + "px";

    popupBody.focus();
    // Put cursor at end for edit mode so the user can append easily.
    const len = popupBody.value.length;
    popupBody.setSelectionRange(len, len);
  }

  function hidePopup() {
    popup.hidden = true;
    popupDelete.hidden = true;
    popupState = null;
    const sel = window.getSelection();
    if (sel) sel.removeAllRanges();
  }

  function openEditPopup(span) {
    const id = span.dataset.commentId;
    const c = findCommentById(id);
    if (!c) return; // Stale span (shouldn't happen but don't crash).

    popupState = { mode: "edit", id };

    const rect = span.getBoundingClientRect();
    showPopup(rect, c.anchorText, c.body);
    popupDelete.hidden = false;
  }

  popupCancel.addEventListener("click", hidePopup);
  popupSave.addEventListener("click", savePopup);
  popupDelete.addEventListener("click", deleteFromPopup);

  popupBody.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      savePopup();
    } else if (e.key === "Escape") {
      e.preventDefault();
      hidePopup();
    }
  });

  function savePopup() {
    if (!popupState) {
      hidePopup();
      return;
    }
    if (popupState.mode === "edit") {
      updateComment(popupState.id);
    } else {
      createComment();
    }
  }

  function createComment() {
    const body = popupBody.value.trim();
    if (!body || !popupState || popupState.mode !== "new") {
      hidePopup();
      return;
    }
    const { range, anchorText, lineStart, lineEnd } = popupState;
    const id = makeCommentId();

    // Wrap selection in a highlight span (best-effort — single range).
    // If the selection crosses element boundaries surroundContents throws;
    // we keep the comment in that case but it won't be clickable to edit.
    // Fixing that is tracked as the "orphan comment list" deferred item.
    try {
      const wrap = document.createElement("span");
      wrap.className = "pr-anchored";
      wrap.dataset.commentId = id;
      wrap.title = "feedback: " + body;
      range.surroundContents(wrap);
    } catch (err) {
      // no highlight; comment is still submitted.
    }

    comments.push({
      id,
      anchorText,
      lineStart: lineStart || 1,
      lineEnd: lineEnd || lineStart || 1,
      body,
    });

    updateCount();
    hidePopup();
  }

  function updateComment(id) {
    const c = findCommentById(id);
    if (!c) {
      hidePopup();
      return;
    }
    const body = popupBody.value.trim();
    if (!body) {
      // Empty body on edit = delete, matches the affordance of "clear and save".
      deleteComment(id);
      hidePopup();
      return;
    }
    c.body = body;
    const span = findAnchoredSpan(id);
    if (span) span.title = "feedback: " + body;
    hidePopup();
  }

  function deleteFromPopup() {
    if (!popupState || popupState.mode !== "edit") return;
    deleteComment(popupState.id);
    hidePopup();
  }

  function deleteComment(id) {
    const idx = comments.findIndex((c) => c.id === id);
    if (idx >= 0) comments.splice(idx, 1);
    const span = findAnchoredSpan(id);
    if (span && span.parentNode) {
      // Unwrap: replace span with its children.
      const parent = span.parentNode;
      while (span.firstChild) parent.insertBefore(span.firstChild, span);
      parent.removeChild(span);
      parent.normalize();
    }
    updateCount();
  }

  function updateCount() {
    countEl.textContent = String(comments.length);
    feedbackBtn.disabled = comments.length === 0;
  }

  // ----- submit -----

  let submitted = false;

  approveBtn.addEventListener("click", function () {
    submit("approve");
  });
  feedbackBtn.addEventListener("click", function () {
    submit("feedback");
  });

  async function submit(action) {
    approveBtn.disabled = true;
    feedbackBtn.disabled = true;
    let resp = null;
    try {
      resp = await fetch("/submit", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": window.__CSRF_TOKEN || "",
        },
        body: JSON.stringify({ action, comments }),
      });
    } catch (err) {
      // Network error. Re-enable the buttons so the user can retry; leave
      // heartbeats running and `submitted` false so the cancel-on-close
      // safety net is still armed.
      approveBtn.disabled = false;
      feedbackBtn.disabled = comments.length === 0;
      return;
    }
    if (resp.status === 409) {
      // Server already concluded (heartbeat timeout, concurrent /cancel).
      // The Go side took the cancel path — surface that to the user.
      submitted = true;
      stopHeartbeat();
      showDone("cancel");
      return;
    }
    submitted = true;
    stopHeartbeat();
    showDone(action);
  }

  // ----- liveness: heartbeat + cancel on unload -----
  //
  // If the user closes the tab without hitting Approve or Send feedback, the
  // Go process would otherwise hang on the Done channel until the 10-minute
  // review timeout. We send a cancel ping on pagehide and a heartbeat every
  // 5s; the server treats either signal as "user is gone, degrade to ask".

  const HEARTBEAT_MS = 5000;
  let heartbeatTimer = null;

  function startHeartbeat() {
    if (heartbeatTimer) return;
    heartbeatTimer = setInterval(() => {
      fetch("/heartbeat", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": window.__CSRF_TOKEN || "",
        },
        body: "{}",
        keepalive: true,
      }).catch(() => {
        // Server already shut down; nothing to do.
      });
    }, HEARTBEAT_MS);
  }

  function stopHeartbeat() {
    if (heartbeatTimer) {
      clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }
  }

  function sendCancel() {
    if (submitted) return;
    // sendBeacon can't set custom headers, so the CSRF token rides in the
    // JSON body.
    const payload = JSON.stringify({ token: window.__CSRF_TOKEN || "" });
    const blob = new Blob([payload], { type: "application/json" });

    // Try sendBeacon first. It returns false if the browser refuses (quota
    // exceeded, payload too big, feature disabled) — in that case fall
    // through to keepalive fetch. Only mark submitted=true once dispatch
    // has succeeded; otherwise the 15s server-side heartbeat timeout is
    // the real safety net.
    let dispatched = false;
    if (navigator.sendBeacon) {
      dispatched = navigator.sendBeacon("/cancel", blob);
    }
    if (!dispatched) {
      try {
        fetch("/cancel", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: payload,
          keepalive: true,
        }).catch(() => {});
        dispatched = true;
      } catch (err) {
        // Fall through; server-side heartbeat timeout will catch us.
      }
    }
    if (dispatched) {
      submitted = true;
      stopHeartbeat();
    }
  }

  // pagehide fires on tab close and navigation away in modern browsers. On
  // iOS backgrounding it can fire but the beacon may be queued and never
  // actually flushed if the tab is killed; the 15s server-side heartbeat
  // timeout is the real safety net there.
  window.addEventListener("pagehide", sendCancel);

  startHeartbeat();

  function showDone(action) {
    const banner = document.createElement("div");
    banner.className = "pr-done";
    let msg;
    switch (action) {
      case "approve":
        msg = "✓ approved — back to claude";
        break;
      case "feedback":
        msg = "✓ sent feedback — claude will revise";
        break;
      case "cancel":
      default:
        msg = "review already concluded — back to claude";
    }
    banner.innerHTML =
      '<div>' + msg + '</div>' +
      '<div class="pr-done-sub">you can close this window</div>';
    document.body.appendChild(banner);
    // Best-effort close.
    setTimeout(() => {
      try { window.close(); } catch (e) {}
    }, 400);
  }

  // ----- TOC scroll-spy -----

  const tocLinks = document.querySelectorAll(".pr-toc a");
  const tocMap = new Map();
  tocLinks.forEach((a) => {
    const id = a.getAttribute("href").slice(1);
    const target = document.getElementById(id);
    if (target) tocMap.set(target, a);
  });

  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) {
          tocLinks.forEach((a) => a.classList.remove("active"));
          const link = tocMap.get(e.target);
          if (link) link.classList.add("active");
        }
      });
    },
    { rootMargin: "-30% 0px -60% 0px" }
  );
  tocMap.forEach((_, el) => observer.observe(el));

  // Close popup on outside click.
  document.addEventListener("mousedown", (e) => {
    if (!popup.hidden && !popup.contains(e.target)) {
      hidePopup();
    }
  });
})();
