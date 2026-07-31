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
  var stored = localStorage.getItem('kute-theme');
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
      localStorage.setItem('kute-theme', root.getAttribute('data-theme'));
      relabel();
    });
  }

  // Copy-to-clipboard for install command
  document.querySelectorAll('[data-copy]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = btn.getAttribute('data-copy');
      navigator.clipboard.writeText(text).then(function () {
        btn.classList.add('copied');
        var original = btn.innerHTML;
        btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20 6L9 17l-5-5"/></svg>';
        setTimeout(function () {
          btn.classList.remove('copied');
          btn.innerHTML = original;
        }, 1600);
      });
    });
  });

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
