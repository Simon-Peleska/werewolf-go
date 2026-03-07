/**
 * <player-card> and <card-list> Web Components — Shadow DOM edition.
 *
 * Shadow DOM keeps idiomorph away from the card's rendered children.
 * The collapsed state lives entirely inside the shadow root (as a class on
 * .pc-card), so idiomorph morphing the host's attributes can never break it.
 *
 * Lobby ± buttons call window.wsSend() (defined in game.html) instead of
 * using <form ws-send>, because ws-send requires htmx to traverse to a
 * [ws-connect] ancestor — which doesn't work across the shadow boundary.
 *
 * Both expanded and collapsed content are always in the DOM. Toggling just
 * swaps opacity/z-index/pointer-events classes and animates the card height.
 * No DOM manipulation during toggle = no flicker.
 */
(function () {
  'use strict';

  // Duration of the cross-fade and height transitions.
  const FADE_DUR = 360; // ms

  /* ─────────────────────────────────────────────────────────────────────────
   * HEAD CSS — host element + card-list grid (Light DOM, injected once)
   * ───────────────────────────────────────────────────────────────────────── */
  const HEAD_STYLES = `
    player-card { display: block; width: 100%; }

    card-list {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
      gap: var(--pico-spacing);
      margin: var(--pico-spacing) 0;
      place-items: center;
    }
    /* Forms inside card-list are invisible to the grid layout */
    card-list > form { display: contents; }
    @media (max-width: 480px) {
      card-list, card-list.win-list { grid-template-columns: 1fr; }
    }

    @keyframes pc-enter { from { opacity: 0; transform: translateY(16px); } to { opacity: 1; transform: translateY(0); } }
    card-list player-card { animation: pc-enter 0.45s ease backwards; }
    card-list player-card:nth-child(1)  { animation-delay: 0.05s; }
    card-list player-card:nth-child(2)  { animation-delay: 0.12s; }
    card-list player-card:nth-child(3)  { animation-delay: 0.19s; }
    card-list player-card:nth-child(4)  { animation-delay: 0.26s; }
    card-list player-card:nth-child(5)  { animation-delay: 0.33s; }
    card-list player-card:nth-child(6)  { animation-delay: 0.40s; }
    card-list player-card:nth-child(7)  { animation-delay: 0.47s; }
    card-list player-card:nth-child(8)  { animation-delay: 0.54s; }
    card-list player-card:nth-child(9)  { animation-delay: 0.61s; }
    card-list player-card:nth-child(10) { animation-delay: 0.68s; }
    card-list player-card:nth-child(11) { animation-delay: 0.75s; }
    card-list player-card:nth-child(12) { animation-delay: 0.82s; }
  `;

  /* ─────────────────────────────────────────────────────────────────────────
   * SHADOW CSS — injected into each card's shadow root.
   * ───────────────────────────────────────────────────────────────────────── */
  const SHADOW_STYLES = `
    :host { display: block; width: 100%; }

    /* ── Card container — visual chrome only ───────────────────────────── */
    .pc-card {
      position: relative;
      background: var(--c-surface);
      border: 1px solid var(--c-border);
      border-radius: 1rem;
      cursor: default;
      box-sizing: border-box;
      overflow: hidden;
      transition: transform 0.15s ease, box-shadow 0.15s ease, border-color 0.15s, border-radius ${FADE_DUR}ms ease-in-out;
    }
    .pc-card::before {
      content: '';
      position: absolute;
      inset: 5px;
      border-radius: calc(1rem - 4px);
      border: 1px solid var(--c-card-inner-glow);
      pointer-events: none;
    }
    .pc-card:hover {
      transform: translateY(-4px);
      box-shadow: 0 10px 32px var(--c-card-hover-shadow), 0 0 0 1px var(--c-card-hover-ring);
    }
    :host([team=villager]) .pc-card { border-color: var(--c-card-team-villager); }
    :host([team=werewolf]) .pc-card { border-color: var(--c-card-team-werewolf); }
    :host([active]) .pc-card {
      border-color: var(--c-flame);
      box-shadow: 0 0 0 1px var(--c-card-active-ring), 0 4px 16px var(--c-card-active-shadow);
    }
    :host([selectable]) .pc-card { cursor: pointer; }
    :host([selected]) .pc-card {
      border-color: var(--c-flame);
      box-shadow: 0 0 0 2px var(--c-card-active-ring), 0 0 28px var(--c-card-active-shadow);
    }
    .pc-card.pc-collapsed { border-radius: 0.75rem; }
    .pc-card.pc-collapsed::before { display: none; }

    /* ── Layers — both always in DOM, one visible, one hidden ──────────── */
    .pc-layer {
      display: flex;
      align-items: center;
      box-sizing: border-box;
      width: 100%;
      transition: opacity ${FADE_DUR}ms ease;
    }
    .pc-active {
      opacity: 1;
      pointer-events: auto;
      z-index: 1;
    }
    .pc-inactive {
      opacity: 0;
      pointer-events: none;
      z-index: 0;
      position: absolute;
      top: 0; left: 0; right: 0;
    }

    /* Expanded layer layout */
    .pc-exp {
      flex-direction: column;
      padding: var(--pico-spacing) calc(var(--pico-spacing) * 0.75) calc(var(--pico-spacing) * 0.6);
    }

    /* Collapsed layer layout */
    .pc-col {
      flex-direction: row;
      gap: 0.65rem;
      padding: 0.5rem 0.75rem;
    }

    /* ── Seal wrap ──────────────────────────────────────────────────────── */
    .pc-seal-wrap {
      position: relative;
      width: 100%;
      aspect-ratio: 1 / 1;
      margin: 0 auto calc(var(--pico-spacing) * 0.6);
      flex-shrink: 0;
    }
    .pc-seal {
      width: 100%; height: 100%;
      object-fit: contain; border-radius: 50%; display: block;
      transition: box-shadow 0.15s;
    }
    :host([team=villager]) .pc-seal {
      box-shadow: 0 0 0 3px var(--c-seal-ring), 0 4px 18px var(--c-seal-shadow);
    }
    :host([team=villager]) .pc-card:hover .pc-seal {
      box-shadow: 0 0 0 3px var(--c-seal-ring-hover), 0 6px 22px var(--c-seal-shadow-hover);
    }
    :host([team=werewolf]) .pc-seal {
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--c-danger) 55%, transparent), 0 4px 18px var(--c-seal-shadow);
    }
    :host([team=werewolf]) .pc-card:hover .pc-seal {
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--c-danger) 80%, transparent), 0 6px 22px var(--c-seal-shadow-hover);
    }

    /* ── Count badge — top-left of seal wrap, expanded only ────────────── */
    .pc-exp .pc-count-wrap {
      position: absolute; top: 0; left: 0;
    }
    .pc-count-wrap.pc-zero { opacity: 0.3; }
    .pc-count {
      font-family: "Metal Mania", var(--pico-font-family-emoji);
      font-size: 1.6em; color: var(--c-amber-bright);
      line-height: 1; text-shadow: 0 0 10px var(--c-count-text-shadow);
      pointer-events: none; z-index: 1; text-align: center; align-content: center;
    }
    .pc-count.pc-zero { color: var(--c-muted); text-shadow: none; }

    /* ── ± buttons, count badge, and heart badge — circular overlays on seal wrap */
    .pc-btn-wrap, .pc-count-wrap, .pc-heart-wrap {
      --bsz: calc(var(--pico-spacing) * 2.2);
      position: absolute; bottom: 0;
      width: var(--bsz); height: var(--bsz);
      border-radius: 50%; overflow: hidden;
      border: 1px solid var(--c-border);
      background: var(--c-surface-2);
      display: flex;
      transition: background 0.1s, border-color 0.1s;
    }
    .pc-btn-minus { left: 0; }
    .pc-btn-plus  { right: 0; }
    .pc-btn-wrap:hover { background: var(--c-bark); border-color: var(--c-flame); }
    .pc-btn-wrap:has(button:disabled) { opacity: 0.3; pointer-events: none; }
    .pc-btn, .pc-count {
      flex: 1; padding: 0; margin: 0; border: none;
      background: transparent; color: var(--c-amber); font-size: 1.2em;
      cursor: pointer; border-radius: 50%;
    }

    /* ── Text ────────────────────────────────────────────────────────────── */
    .pc-name {
      font-family: "Metal Mania", var(--pico-font-family-emoji);
      font-size: 1em; color: var(--c-amber-bright);
      text-align: center; margin: calc(var(--pico-spacing) * 0.3) 0 0; line-height: 1.2;
      width: 100%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
    }
    /* Role name hidden in expanded mode (it's visible in the seal image) */
    .pc-exp .pc-role { display: none; }
    .pc-role { font-size: 0.8em; color: var(--c-amber); text-align: center; margin: 0.1em 0 0; }
    .pc-desc {
      font-size: 0.72em; color: var(--c-muted); text-align: center;
      line-height: 1.35;
      margin: calc(var(--pico-spacing) * 0.3) 0 calc(var(--pico-spacing) * 0.4);
      flex: 1;
      display: -webkit-box; -webkit-line-clamp: 3;
      -webkit-box-orient: vertical; overflow: hidden;
    }
    .pc-footer {
      display: flex; justify-content: center; align-items: center;
      width: 100%; border-top: 1px solid var(--c-border);
      padding-top: calc(var(--pico-spacing) * 0.4); margin-top: auto;
    }
    .pc-team, .pc-alive, .pc-dead {
      font-size: 0.65em; text-transform: uppercase;
      letter-spacing: 0.06em; font-style: italic; color: var(--c-muted);
    }
    :host([team=villager]) .pc-team { color: var(--c-team-villager-label); }
    :host([team=werewolf]) .pc-team { color: var(--c-team-werewolf-label); }
    :host([team=villager]) .pc-col .pc-role { color: var(--c-team-villager-label); }
    :host([team=werewolf]) .pc-col .pc-role { color: var(--c-team-werewolf-label); }

    /* ── Collapse toggle button — absolute top-right of pc-seal-wrap ─────── */
    .pc-btn-collapse { top: 0; right: 0; }
    .pc-uncollapse {
      background: var(--c-surface-2); border: 1px solid var(--c-border); color: var(--c-muted);
      cursor: pointer; display: flex; align-items: center; justify-content: center;
      padding: 0; margin: 0; font-size: 0.85em; line-height: 1; z-index: 2;
      transition: background 0.15s, border-color 0.15s, color 0.15s;
      width: 26px; height: 26px; border-radius: 50%;
    }
    .pc-toggle:hover { background: var(--c-bark); border-color: var(--c-flame); color: var(--c-amber); }

    /* ── Dead player ─────────────────────────────────────────────────────── */
    :host([alive=false]) .pc-card { opacity: 0.55; filter: grayscale(50%); }

    /* ── Win screen variants ────────────────────────────────────────────── */
    :host([loser]) .pc-card { opacity: 0.42; filter: grayscale(35%); }
    :host([winner][team=villager]) .pc-card {
      box-shadow: 0 0 0 1px var(--c-card-hover-ring), 0 4px 16px var(--c-card-hover-shadow);
    }
    :host([winner][team=werewolf]) .pc-card {
      box-shadow: 0 0 0 1px var(--c-vote-selected-ring), 0 4px 16px var(--c-card-hover-shadow);
    }

    /* ── Lover styling ───────────────────────────────────────────────────── */
    :host([lover]) .pc-card {
      border-color: var(--c-lover-border);
      box-shadow: 0 0 0 1px var(--c-lover-ring), 0 4px 16px var(--c-lover-shadow);
    }
    .pc-heart-wrap { right: 0; }
    .pc-heart {
      flex: 1; text-align: center; align-content: center;
      font-size: 1.1em; pointer-events: none; color: var(--c-lover-heart);
    }
    :host([lover]) .pc-heart-wrap {
      background: var(--c-lover-badge-bg);
      border-color: var(--c-lover-border);
    }
    .pc-col .pc-heart-wrap { position: static; --bsz: 28px; flex-shrink: 0; }

    /* ── Collapsed layer descendant overrides ──────────────────────────── */
    .pc-col .pc-seal-wrap {
      height: 44px !important; width: 44px !important;
      aspect-ratio: auto; margin: 0; flex-shrink: 0;
    }
    .pc-col .pc-seal { width: 44px !important; height: 44px !important; flex-shrink: 0; }

    :host([lobby]) .pc-col .pc-btn-wrap,
    .pc-col .pc-count-wrap {
      position: static; --bsz: 28px; flex-shrink: 0;
    }
    .pc-col .pc-count { font-size: 1.1em; text-shadow: none; }

    .pc-col .pc-info {
      flex: 1; min-width: 0;
      font-size: 0.82em;
      white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
      margin: 0;
    }
    .pc-col .pc-info .pc-name { color: var(--c-amber); }
    .pc-col .pc-info .pc-sep  { color: var(--c-muted); }
    .pc-col .pc-info .pc-role { color: var(--c-amber); }
    :host([team=villager]) .pc-col .pc-info .pc-role { color: var(--c-team-villager-label); }
    :host([team=werewolf]) .pc-col .pc-info .pc-role { color: var(--c-team-werewolf-label); }
    .pc-col .pc-desc { display: none; }
    .pc-col .pc-footer { width: auto; border-top: none; padding-top: 0; margin-top: 0; flex-shrink: 0; }
    .pc-col .pc-status { display: none; }
  `;

  if (!document.getElementById('player-card-styles')) {
    const s = document.createElement('style');
    s.id = 'player-card-styles';
    s.textContent = HEAD_STYLES;
    document.head.appendChild(s);
  }

  /* ─────────────────────────────────────────────────────────────────────────
   * Helpers
   * ───────────────────────────────────────────────────────────────────────── */
  function esc(v) {
    return String(v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function roleSeal(name) {
    return '/static/seals/' + name.replace(/ /g, '_') + '.webp';
  }

  /* ─────────────────────────────────────────────────────────────────────────
   * <player-card> Custom Element
   * ───────────────────────────────────────────────────────────────────────── */
  class PlayerCard extends HTMLElement {
    static get observedAttributes() {
      return [
        'role-name', 'role-desc', 'team', 'player-name',
        'count', 'role-id', 'lobby', 'total-roles', 'player-count', 'active',
        'winner', 'loser', 'alive', 'lover', 'selected', 'selectable',
      ];
    }

    constructor() {
      super();
      this._collapsed     = false;
      this._ready         = false;
      this._transitioning = false;
    }

    connectedCallback() {
      if (!this._ready) {
        this._ready = true;
        if (this.hasAttribute('collapsed') || window.innerWidth < 576) {
          this._collapsed = true;
        }
        const shadow = this.attachShadow({ mode: 'open' });
        const style = document.createElement('style');
        style.textContent = SHADOW_STYLES;
        shadow.appendChild(style);
      }
      this._render();
    }

    attributeChangedCallback() {
      if (this._ready && !this._transitioning) this._render();
    }

    // Build the inner HTML for one layer (expanded or collapsed).
    _buildLayerContent(collapsed) {
      const roleName   = this.getAttribute('role-name')    || '';
      const playerName = this.getAttribute('player-name')  || '';
      const roleDesc   = this.getAttribute('role-desc')    || '';
      const team       = this.getAttribute('team')         || '';
      const countAttr  = this.getAttribute('count');
      const count      = countAttr ?? '0';
      const roleId     = this.getAttribute('role-id')      || '';
      const isLobby    = this.hasAttribute('lobby');
      const totRoles   = parseInt(this.getAttribute('total-roles')  || '0');
      const plrCount   = parseInt(this.getAttribute('player-count') || '0');
      const aliveAttr  = this.getAttribute('alive');
      const isLover    = this.hasAttribute('lover');

      const addDis = (plrCount > 0 && totRoles >= plrCount) ? ' disabled' : '';
      const remDis = count === '0' ? ' disabled' : '';
      const seal   = roleSeal(roleName);
      const toggleCall = `this.getRootNode().host._toggle()`;
      const heartBadge = `<div class="pc-heart-wrap"><span class="pc-heart">💞</span></div>`;

      let h = '';

      if (!collapsed) {
        // ── Expanded content ──
        h += `<div class="pc-seal-wrap">`;
        h += `<img class="pc-seal" src="${seal}" alt="${esc(roleName)}">`;
        if (isLobby) {
          h += `<div class="pc-btn-wrap pc-btn-minus">`
             +   `<button class="pc-btn"${remDis} onclick="window.wsSend({action:'update_role',role_id:'${esc(roleId)}',delta:'-1'})">−</button>`
             + `</div>`;
          h += `<div class="pc-count-wrap${count === '0' ? ' pc-zero' : ''}">`
             +   `<span class="pc-count${count === '0' ? ' pc-zero' : ''}">${esc(count)}</span>`
             + `</div>`;
          h += `<div class="pc-btn-wrap pc-btn-plus">`
             +   `<button class="pc-btn"${addDis} onclick="window.wsSend({action:'update_role',role_id:'${esc(roleId)}',delta:'1'})">+</button>`
             + `</div>`;
        } else if (countAttr !== null) {
          h += `<div class="pc-count-wrap${count === '0' ? ' pc-zero' : ''}">`
             +   `<span class="pc-count${count === '0' ? ' pc-zero' : ''}">${esc(count)}</span>`
             + `</div>`;
        }
        if (isLover) h += heartBadge;
        h += `<div class="pc-btn-wrap pc-btn-collapse">`
           +   `<button class="pc-collapse pc-btn" onclick="event.stopPropagation();${toggleCall}" aria-label="Collapse">&#9650;</button>`
           + `</div>`;
        h += `</div>`;
        if (playerName) h += `<span class="pc-name">${esc(playerName)}</span>`;
        if (roleName)   h += `<span class="pc-role">${esc(roleName)}</span>`;
        if (roleDesc)   h += `<p class="pc-desc">${esc(roleDesc)}</p>`;
        h += `<div class="pc-footer"><span class="pc-team">${esc(team)}</span>`;
        if (aliveAttr !== null) {
          const alive = aliveAttr === 'true';
          h += `<span class="${alive ? 'pc-alive' : 'pc-dead'}">&nbsp;| ${alive ? 'Alive' : 'Dead'}</span>`;
        }
        h += `</div>`;
      } else {
        // ── Collapsed content ──
        h += `<div class="pc-seal-wrap"><img class="pc-seal" src="${seal}" alt="${esc(roleName)}"></div>`;
        const sep = `<span class="pc-sep"> | </span>`;
        let infoParts = [];
        if (playerName) infoParts.push(`<span class="pc-name">${esc(playerName)}</span>`);
        if (roleName && team !== 'unknown') infoParts.push(`<span class="pc-role">${esc(roleName)}</span>`);
        if (aliveAttr !== null && aliveAttr !== 'true') infoParts.push(`<span class="pc-dead">Dead</span>`);
        if (infoParts.length) h += `<span class="pc-info">${infoParts.join(sep)}</span>`;
        if (countAttr !== null) {
          h += `<div class="pc-count-wrap${count === '0' ? ' pc-zero' : ''}">`
             +   `<span class="pc-count${count === '0' ? ' pc-zero' : ''}">${esc(count)}</span>`
             + `</div>`;
        }
        if (isLover) h += heartBadge;
        h += `<button class="pc-toggle pc-uncollapse" onclick="event.stopPropagation();${toggleCall}" aria-label="Expand">&#9660;</button>`;
      }

      return h;
    }

    // Build the full card element with both layers. Does NOT touch the DOM.
    _buildCardElement() {
      const expActive = !this._collapsed ? 'pc-active' : 'pc-inactive';
      const colActive =  this._collapsed ? 'pc-active' : 'pc-inactive';
      const toggleCall = `this.getRootNode().host._toggle()`;

      let h = `<div class="pc-card${this._collapsed ? ' pc-collapsed' : ''}">`;
      h += `<div class="pc-layer pc-exp ${expActive}">${this._buildLayerContent(false)}</div>`;
      h += `<div class="pc-layer pc-col ${colActive}" onclick="${toggleCall}">${this._buildLayerContent(true)}</div>`;
      h += `</div>`;

      const tmp = document.createElement('div');
      tmp.innerHTML = h;
      return tmp.firstElementChild;
    }

    _toggle() {
      if (this._transitioning) return;
      this._transitioning = true;

      const shadow = this.shadowRoot;
      const card = shadow.querySelector('.pc-card');
      const startH = card.offsetHeight;

      // Pin height so the swap doesn't cause a layout jump.
      card.style.height = startH + 'px';

      // Flip state.
      this._collapsed = !this._collapsed;

      // Toggle card-level class (border-radius).
      card.classList.toggle('pc-collapsed', this._collapsed);

      // Swap layer visibility — just class toggles, no DOM changes.
      const exp = card.querySelector('.pc-exp');
      const col = card.querySelector('.pc-col');

      if (this._collapsed) {
        exp.classList.replace('pc-active', 'pc-inactive');
        col.classList.replace('pc-inactive', 'pc-active');
      } else {
        col.classList.replace('pc-active', 'pc-inactive');
        exp.classList.replace('pc-inactive', 'pc-active');
      }

      // Measure target height (active layer is now in flow).
      card.style.height = 'auto';
      const endH = card.offsetHeight;
      card.style.height = startH + 'px';

      // Force reflow so browser records startH as transition origin.
      void card.offsetHeight;

      // Animate card height.
      card.style.transition = `height ${FADE_DUR}ms ease-in-out`;
      card.style.height = endH + 'px';

      // Cleanup after animation completes.
      setTimeout(() => {
        card.style.cssText = '';
        this._transitioning = false;
      }, FADE_DUR);
    }

    _render() {
      const shadow = this.shadowRoot;
      if (!shadow) return;

      const newCard = this._buildCardElement();
      const oldCard = shadow.querySelector('.pc-card');
      if (oldCard && typeof Idiomorph !== 'undefined') {
        Idiomorph.morph(oldCard, newCard);
      } else if (oldCard) {
        shadow.replaceChild(newCard, oldCard);
      } else {
        shadow.appendChild(newCard);
      }
    }
  }

  customElements.define('player-card', PlayerCard);

  /* ─────────────────────────────────────────────────────────────────────────
   * <card-list> — grid wrapper; styling via HEAD_STYLES above
   * ───────────────────────────────────────────────────────────────────────── */
  class CardList extends HTMLElement {}
  customElements.define('card-list', CardList);
})();
