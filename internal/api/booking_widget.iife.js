/* Booking widget — vanilla IIFE, no framework, CSP-safe (script-src 'self').
 * Mounts into #vc-booking-widget, reads data-restaurant-id (+ optional
 * data-api-base). All API calls are same-origin under <base>/widget/*.
 * Colors come from CSS vars --widget-* set by the template stylesheet. */
(function () {
  'use strict';
  var LOAD_TIME = Math.floor(Date.now() / 1000); // honeypot: >=5s by submit time

  function boot() {
    var mount = document.getElementById('vc-booking-widget');
    if (!mount) return; // safe no-op on pages without the widget
    var rid = mount.getAttribute('data-restaurant-id') || '1';
    var apiBase = (mount.getAttribute('data-api-base') || '') + '/widget';
    var q = '?restaurant_id=' + encodeURIComponent(rid);

    // Any .hero-cta scrolls to the widget.
    var ctas = document.querySelectorAll('.hero-cta');
    for (var i = 0; i < ctas.length; i++) {
      ctas[i].addEventListener('click', function (e) {
        e.preventDefault();
        mount.scrollIntoView({ behavior: 'smooth', block: 'center' });
      });
    }

    mount.innerHTML =
      '<form class="vcw-form" novalidate>' +
      '<div class="vcw-row"><label>Fecha<input type="date" name="reservation_date" required></label>' +
      '<label>Comensales<input type="number" name="party_size" min="2" max="20" value="2" required></label></div>' +
      '<div class="vcw-row"><label>Hora<select name="reservation_time" required><option value="">—</option></select></label>' +
      '<label>Nombre<input type="text" name="customer_name" required></label></div>' +
      '<div class="vcw-row"><label>Teléfono<input type="tel" name="contact_phone" required></label>' +
      '<label class="vcw-cc">Prefijo<input type="text" name="country_code" value="+34"></label></div>' +
      '<input type="text" name="website_url" tabindex="-1" autocomplete="off" style="position:absolute;left:-9999px" aria-hidden="true">' +
      '<button type="submit" class="vcw-submit">Reservar mesa</button>' +
      '<p class="vcw-msg" role="status"></p>' +
      '</form>';

    var form = mount.querySelector('.vcw-form');
    var dateEl = form.reservation_date;
    var timeEl = form.reservation_time;
    var msg = mount.querySelector('.vcw-msg');
    var today = new Date().toISOString().slice(0, 10);
    dateEl.min = today;

    function setMsg(text, kind) {
      msg.textContent = text || '';
      msg.className = 'vcw-msg' + (kind ? ' vcw-msg-' + kind : '');
    }

    // Populate the time select from availability for the chosen date.
    function loadHours() {
      timeEl.innerHTML = '<option value="">Cargando…</option>';
      fetch(apiBase + '/reservations/hour-data' + q + '&date=' + encodeURIComponent(dateEl.value))
        .then(function (r) { return r.json(); })
        .then(function (data) {
          var hours = (data && data.activeHours) || [];
          var status = (data && data.hourData) || {};
          var opts = '<option value="">Selecciona hora</option>';
          for (var j = 0; j < hours.length; j++) {
            var h = hours[j];
            var s = status[h];
            if (s && (s.isClosed || s.status === 'full')) continue;
            opts += '<option value="' + h + '">' + h + '</option>';
          }
          timeEl.innerHTML = opts;
        })
        .catch(function () { timeEl.innerHTML = '<option value="">Sin disponibilidad</option>'; });
    }
    dateEl.addEventListener('change', loadHours);

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      setMsg('Enviando…', '');
      var btn = form.querySelector('.vcw-submit');
      btn.disabled = true;
      var body = new URLSearchParams();
      body.set('reservation_date', dateEl.value);
      body.set('party_size', form.party_size.value);
      body.set('reservation_time', timeEl.value);
      body.set('customer_name', form.customer_name.value);
      body.set('contact_phone', form.contact_phone.value);
      body.set('country_code', form.country_code.value);
      body.set('website_url', form.website_url.value);
      body.set('form_load_time', String(LOAD_TIME));
      fetch(apiBase + '/bookings/front' + q, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      })
        .then(function (r) { return r.json().then(function (d) { return { ok: r.ok, d: d }; }); })
        .then(function (res) {
          if (res.ok && res.d && res.d.success !== false) {
            form.reset();
            setMsg('¡Reserva recibida! Te confirmaremos en breve.', 'ok');
          } else {
            setMsg((res.d && res.d.message) || 'No se pudo completar la reserva.', 'err');
          }
        })
        .catch(function () { setMsg('Error de red. Inténtalo de nuevo.', 'err'); })
        .then(function () { btn.disabled = false; });
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
