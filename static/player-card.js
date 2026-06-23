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
          // pc-seal-profile forces opacity:1 unconditionally, so the wrapper's
          // lqip/seal-loaded state no longer matters visually — clear it anyway
          // so a later role reveal doesn't inherit a stale placeholder.
          card.querySelectorAll('.pc-seal').forEach(function (img) {
            img.src = url;
            img.classList.add('pc-seal-profile');
            var wrap = img.closest('.pc-seal-wrap');
            if (wrap) {
              wrap.classList.remove('lqip', 'seal-loaded');
              wrap.style.backgroundImage = '';
            }
          });
        }
      })
      .catch(function (err) { console.error('Profile image upload failed:', err); });
    input.value = '';
  }

  // For cached images onload never fires, and idiomorph re-renders (e.g. a lobby
  // role-count change) reset the wrapper's class to the server's markup even when
  // the image itself was preserved/already loaded — check .complete on every render.
  function pcFixSealLqip() {
    document.querySelectorAll('.pc-seal-wrap.lqip').forEach(function (wrap) {
      var img = wrap.querySelector('img.pc-seal');
      if (img && img.complete) wrap.classList.add('seal-loaded');
    });
    // game-card-seal (index.html "your games" list) still uses the single-element
    // background-image technique, so the .lqip/.seal-loaded pair lives on the img itself.
    document.querySelectorAll('img.lqip').forEach(function (img) {
      if (img.complete) img.classList.add('seal-loaded');
    });
  }

  // Voter-chip list only fades the edge it can actually scroll towards —
  // top fade once you've scrolled past the first row, bottom fade only
  // while more chips are still hidden below.
  function pcUpdateVoterFade(el) {
    el.classList.toggle('pc-voters-fade-top', el.scrollTop > 0);
    el.classList.toggle('pc-voters-fade-bottom', el.scrollTop + el.clientHeight < el.scrollHeight - 1);
  }

  function pcInitVoterScroll() {
    document.querySelectorAll('.pc-voters').forEach(function (el) {
      if (!el.dataset.scrollBound) {
        el.dataset.scrollBound = '1';
        el.addEventListener('scroll', function () { pcUpdateVoterFade(el); }, { passive: true });
      }
      pcUpdateVoterFade(el);
    });
  }

  document.addEventListener('DOMContentLoaded', function () {
    pcFixSealLqip();
    pcInitVoterScroll();
    document.body.addEventListener('htmx:wsAfterMessage', function () {
      pcFixSealLqip();
      pcInitVoterScroll();
    });
  });

  window.pcToggle = pcToggle;
  window.pcUploadClick = pcUploadClick;
  window.pcUploadChange = pcUploadChange;
})();
