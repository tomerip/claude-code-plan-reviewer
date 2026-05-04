(function () {
  "use strict";

  const comments = [];

  const doc = document.getElementById("pr-doc");
  const popup = document.getElementById("pr-popup");
  const popupAnchorText = document.getElementById("pr-popup-anchor-text");
  const popupBody = document.getElementById("pr-popup-body");
  const popupSave = document.getElementById("pr-popup-save");
  const popupCancel = document.getElementById("pr-popup-cancel");
  const countEl = document.getElementById("pr-count");
  const approveBtn = document.getElementById("pr-approve");
  const feedbackBtn = document.getElementById("pr-feedback");

  let pendingSelection = null; // {range, anchorText, lineStart, lineEnd}

  // ----- text selection → popup -----

  doc.addEventListener("mouseup", function () {
    setTimeout(handleSelection, 0);
  });

  function handleSelection() {
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

    pendingSelection = {
      range: range.cloneRange(),
      anchorText: text,
      lineStart,
      lineEnd,
    };

    const rect = range.getBoundingClientRect();
    showPopup(rect, text);
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

  function showPopup(rect, anchorText) {
    popup.hidden = false;
    popupAnchorText.textContent =
      anchorText.length > 60 ? anchorText.slice(0, 60) + "…" : anchorText;
    popupBody.value = "";

    const top = window.scrollY + rect.bottom + 6;
    const left = Math.min(
      window.scrollX + rect.left,
      window.innerWidth - popup.offsetWidth - 20
    );
    popup.style.top = top + "px";
    popup.style.left = Math.max(left, 10) + "px";

    popupBody.focus();
  }

  function hidePopup() {
    popup.hidden = true;
    pendingSelection = null;
    window.getSelection().removeAllRanges();
  }

  popupCancel.addEventListener("click", hidePopup);

  popupSave.addEventListener("click", saveComment);

  popupBody.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      saveComment();
    } else if (e.key === "Escape") {
      e.preventDefault();
      hidePopup();
    }
  });

  function saveComment() {
    const body = popupBody.value.trim();
    if (!body || !pendingSelection) {
      hidePopup();
      return;
    }
    const { range, anchorText, lineStart, lineEnd } = pendingSelection;

    // Wrap selection in a highlight span (best-effort — single range).
    try {
      const wrap = document.createElement("span");
      wrap.className = "pr-anchored";
      wrap.title = "feedback: " + body;
      range.surroundContents(wrap);
    } catch (err) {
      // Selection crosses element boundaries; skip highlight but keep the comment.
    }

    comments.push({
      anchorText,
      lineStart: lineStart || 1,
      lineEnd: lineEnd || lineStart || 1,
      body,
    });

    updateCount();
    hidePopup();
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
