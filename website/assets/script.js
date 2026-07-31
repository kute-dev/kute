(function () {
  // Sticky nav background on scroll
  var nav = document.querySelector('.nav');
  var onScroll = function () {
    if (window.scrollY > 8) nav.classList.add('scrolled');
    else nav.classList.remove('scrolled');
  };
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });

  // Mobile nav toggle
  var toggle = document.querySelector('.nav-toggle');
  var links = document.querySelector('.nav-links');
  if (toggle && links) {
    var setOpen = function (open) {
      links.classList.toggle('open', open);
      links.style.display = open ? 'flex' : '';
      // The button's own state, not just the panel's: without this a screen
      // reader announces the same thing whether the menu is open or shut.
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    };
    toggle.addEventListener('click', function () {
      setOpen(!links.classList.contains('open'));
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && links.classList.contains('open')) {
        setOpen(false);
        toggle.focus();
      }
    });
    // An open menu covering the section you just jumped to is a trap on a
    // phone, so following a link closes it.
    links.addEventListener('click', function (e) {
      if (e.target && e.target.closest && e.target.closest('a')) setOpen(false);
    });
  }

  // Theme toggle (dark/light), persisted
  var themeBtn = document.querySelector('[data-theme-toggle]');
  var root = document.documentElement;
  var currentTheme = function () {
    return root.getAttribute('data-theme') ||
      (window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
  };
  // localStorage throws, not returns null, when site data is blocked (Safari
  // private mode). Unguarded it killed this whole IIFE, and since the copy
  // buttons and scroll reveal are registered further down, a storage
  // exception left every .reveal section stuck at opacity 0 — a blank page.
  var store = {
    get: function (k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set: function (k, v) { try { localStorage.setItem(k, v); } catch (e) {} }
  };
  var stored = store.get('kute-theme');
  if (stored) root.setAttribute('data-theme', stored);
  if (themeBtn) {
    // Name the action rather than the control: a static "Toggle theme" never
    // told anyone which theme they were in or what pressing it would do.
    var relabel = function () {
      themeBtn.setAttribute('aria-label',
        currentTheme() === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
    };
    relabel();
    themeBtn.addEventListener('click', function () {
      root.setAttribute('data-theme', currentTheme() === 'dark' ? 'light' : 'dark');
      store.set('kute-theme', root.getAttribute('data-theme'));
      relabel();
    });
  }

  // Copy-to-clipboard for install commands.
  // A live region carries the result: swapping the icon says nothing to a
  // screen reader, and the button's own label stays "Copy command" either way.
  var announcer = document.createElement('div');
  announcer.setAttribute('aria-live', 'polite');
  announcer.setAttribute('role', 'status');
  announcer.className = 'visually-hidden';
  document.body.appendChild(announcer);

  var TICK = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M20 6L9 17l-5-5"/></svg>';

  var copyText = function (text) {
    // navigator.clipboard is undefined outside a secure context, so over
    // file:// or plain http this throws rather than rejecting.
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      var ok = false;
      try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
      document.body.removeChild(ta);
      ok ? resolve() : reject(new Error('copy unavailable'));
    });
  };

  document.querySelectorAll('[data-copy]').forEach(function (btn) {
    // Captured once, at wire-up. Reading it inside the handler meant a second
    // click during the 1600ms window captured the tick and restored *that*,
    // leaving the button stuck as a checkmark.
    var original = btn.innerHTML;
    var timer = null;
    btn.addEventListener('click', function () {
      copyText(btn.getAttribute('data-copy')).then(function () {
        btn.classList.add('copied');
        btn.innerHTML = TICK;
        announcer.textContent = 'Copied to clipboard';
        clearTimeout(timer);
        timer = setTimeout(function () {
          btn.classList.remove('copied');
          btn.innerHTML = original;
          announcer.textContent = '';
        }, 1600);
      }).catch(function () {
        // Previously an unhandled rejection: the button simply did nothing
        // and never said why.
        announcer.textContent = 'Copy failed — select the command and copy it manually';
      });
    });
  });

  // Demo videos. The markup deliberately carries no autoplay attribute: these
  // replaced auto-looping GIFs, which had no way to stop them, and a visitor
  // who has asked for reduced motion should not be handed the same thing in a
  // different container. They stay paused on their poster with controls, and
  // play only if motion is welcome — and only while actually on screen, so
  // five terminal recordings aren't decoding at once.
  var demos = document.querySelectorAll('video[data-demo]');
  if (demos.length) {
    var wantsMotion = !(window.matchMedia &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches);
    if (wantsMotion && 'IntersectionObserver' in window) {
      var vio = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          var v = entry.target;
          if (entry.isIntersecting) {
            // preload="none" until we actually want it.
            if (v.preload !== 'auto') v.preload = 'auto';
            // Rejects if the browser blocks autoplay; the controls remain.
            var p = v.play();
            if (p && p.catch) p.catch(function () {});
          } else if (!v.paused) {
            v.pause();
          }
        });
      }, { threshold: 0.25 });
      demos.forEach(function (v) { vio.observe(v); });
    }
  }

  // Guide contents rail: mark the section currently being read. The guide is
  // long enough that "where am I" is a real question, and the rail is the only
  // thing on the page that can answer it. Purely additive — every link still
  // works as an anchor without this.
  var railLinks = document.querySelectorAll('.guide-rail a[href^="#"]');
  if (railLinks.length && 'IntersectionObserver' in window) {
    var byId = {};
    var sections = [];
    railLinks.forEach(function (a) {
      var el = document.getElementById(a.getAttribute('href').slice(1));
      if (el) { byId[el.id] = a; sections.push(el); }
    });
    var visible = {};
    var mark = function () {
      // Topmost visible section wins. Two are on screen at once for most of a
      // scroll, and picking the last one to intersect would flip the highlight
      // backwards when scrolling up.
      var current = null;
      sections.forEach(function (el) {
        if (visible[el.id] && !current) current = el.id;
      });
      railLinks.forEach(function (a) { a.removeAttribute('aria-current'); });
      if (current && byId[current]) byId[current].setAttribute('aria-current', 'true');
    };
    var sio = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) { visible[entry.target.id] = entry.isIntersecting; });
      mark();
    }, { rootMargin: '-88px 0px -60% 0px' });
    sections.forEach(function (el) { sio.observe(el); });
    // The first callback fires while the browser is still scrolling to a
    // #hash, so a deep link lands with the previous section highlighted until
    // you scroll. Re-check once everything has settled.
    window.addEventListener('load', mark);
  }

  // Scroll reveal
  var revealEls = document.querySelectorAll('.reveal');
  if ('IntersectionObserver' in window && revealEls.length) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('in');
          io.unobserve(entry.target);
        }
      });
    }, { threshold: 0.12 });
    revealEls.forEach(function (el) { io.observe(el); });
  } else {
    revealEls.forEach(function (el) { el.classList.add('in'); });
  }
})();
