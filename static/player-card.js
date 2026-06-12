/**
 * player-card behaviour — plain functions (no custom element / shadow DOM).
 *
 * The card markup is server-rendered by templates/player_card.html and styled by
 * .player-card rules in static/style.css. These helpers only handle the two bits
 * of interactivity the template wires up via inline onclick/onchange:
 *
 *   pcToggle(el)       expand/collapse a card (animates its height)
 *   pcUploadClick(el)  open the hidden file input on the viewer's own card
 *   pcUploadChange(el) upload the chosen profile image and swap the seal
 *
 * Both the expanded (.pc-exp) and collapsed (.pc-col) layers are always present;
 * toggling just swaps the pc-active/pc-inactive classes and animates the height.
 */
(function () {
  'use strict';

  // Duration of the height transition (must match the CSS layer fade).
  const FADE_DUR = 360; // ms

  function pcToggle(el) {
    const card = el.closest('.player-card');
    if (!card || card._pcTransitioning) return;
    card._pcTransitioning = true;

    const exp = card.querySelector('.pc-exp');
    const col = card.querySelector('.pc-col');
    const isCollapsed = card.classList.contains('pc-collapsed');

    // Height of the currently visible layer (the card's current height).
    const startH = card.offsetHeight;

    // Swap which layer is active.
    if (isCollapsed) {
      col.classList.replace('pc-active', 'pc-inactive');
      exp.classList.replace('pc-inactive', 'pc-active');
    } else {
      exp.classList.replace('pc-active', 'pc-inactive');
      col.classList.replace('pc-inactive', 'pc-active');
    }
    card.classList.toggle('pc-collapsed', !isCollapsed);

    // Measure the new natural height with the new layer in flow.
    card.style.height = 'auto';
    const endH = card.offsetHeight;

    // Pin the start height, force a reflow, then animate to the end height.
    card.style.height = startH + 'px';
    void card.offsetHeight;
    card.style.transition = `height ${FADE_DUR}ms ease-in-out`;
    card.style.height = endH + 'px';

    setTimeout(function () {
      card.style.cssText = '';
      card._pcTransitioning = false;
    }, FADE_DUR);
  }

  function pcUploadClick(el) {
    const card = el.closest('.player-card');
    if (!card) return;
    const input = card.querySelector('input[type=file]');
    if (input) input.click();
  }

  function pcUploadChange(input) {
    const file = input.files && input.files[0];
    if (!file) return;
    const card = input.closest('.player-card');
    const fd = new FormData();
    fd.append('image', file);
    fetch('/player/upload-image', { method: 'POST', body: fd })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (data) {
        if (data && data.image_id && card) {
          const url = '/player-image/' + data.image_id;
          card.setAttribute('profile-image', url);
          // Swap every seal img (both layers) over to the new profile image.
          card.querySelectorAll('.pc-seal').forEach(function (img) {
            img.src = url;
            img.classList.add('pc-seal-profile');
            img.classList.remove('lqip', 'seal-loaded');
            img.style.backgroundImage = '';
          });
        }
      })
      .catch(function (err) { console.error('Profile image upload failed:', err); });
    input.value = '';
  }

  // On narrow screens, collapse cards that carry a collapse toggle (lobby role
  // cards) so the grid stays compact — matching the old web component, which
  // started collapsed when innerWidth < 576. Runs once on load and after each
  // WS render. We mark cards we've already auto-collapsed so re-renders don't
  // fight a user who manually re-expanded one.
  function pcAutoCollapseMobile() {
    if (window.innerWidth >= 576) return;
    document.querySelectorAll('.player-card .pc-btn-collapse').forEach(function (btn) {
      const card = btn.closest('.player-card');
      if (!card || card._pcAutoCollapsed) return;
      card._pcAutoCollapsed = true;
      if (!card.classList.contains('pc-collapsed')) pcToggle(btn);
    });
  }

  // For cached images onload never fires — check .complete on every render.
  function pcFixSealLqip() {
    document.querySelectorAll('img.lqip').forEach(function (img) {
      if (img.complete) img.classList.add('seal-loaded');
    });
  }

  document.addEventListener('DOMContentLoaded', function () {
    pcAutoCollapseMobile();
    pcFixSealLqip();
    document.body.addEventListener('htmx:wsAfterMessage', pcAutoCollapseMobile);
    document.body.addEventListener('htmx:wsAfterMessage', pcFixSealLqip);
  });

  window.pcToggle = pcToggle;
  window.pcUploadClick = pcUploadClick;
  window.pcUploadChange = pcUploadChange;
})();
