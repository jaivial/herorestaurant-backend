package api

// Coordination id: ads_canvas_v1
// Cross-origin embed API + JS plugin for businesses whose website is not
// built on our templates: they paste one <script> tag and the plugin renders
// their active canvas ads in iframes served by this backend.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// handlePublicAdsEmbed serves GET /public/ads/embed?restaurant_id=X[&date=YYYY-MM-DD].
// CORS-open read-only endpoint: returns active canvas ads with their render URLs.
func (s *Server) handlePublicAdsEmbed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	restaurantID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("restaurant_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid restaurant_id"})
			return
		}
		restaurantID = id
	} else if id, ok := s.resolveRestaurantID(r); ok {
		restaurantID = id
	}
	if restaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM restaurant_ads WHERE restaurant_id = ? AND active = 1 AND canvas_json IS NOT NULL ORDER BY updated_at DESC, id DESC`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ads")
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	type embedVariant struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	type embedAd struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		RenderURL string `json:"render_url"`
		// Coord id ads_canvas_breakpoints_v1: responsive dims + popover chrome
		// so the plugin can pick a variant by viewport like the home popover does.
		Variants  map[string]embedVariant `json:"variants,omitempty"`
		Container *boAdCanvasContainer    `json:"container,omitempty"`
	}
	ads := []embedAd{}
	for _, id := range ids {
		ad, err := s.readBOAd(r.Context(), restaurantID, id)
		if err != nil || ad.Canvas == nil {
			continue
		}
		if !publicAdVisibleOnDate(ad, date) {
			continue
		}
		item := embedAd{
			ID:        ad.ID,
			Name:      ad.Name,
			Width:     ad.Canvas.Width,
			Height:    ad.Canvas.Height,
			RenderURL: fmt.Sprintf("/api/public/ads/%d/render?restaurant_id=%d&src=plugin", ad.ID, restaurantID),
		}
		for name, variant := range ad.Canvas.Variants {
			if variant == nil || variant.Width <= 0 || variant.Height <= 0 {
				continue
			}
			if item.Variants == nil {
				item.Variants = map[string]embedVariant{}
			}
			item.Variants[name] = embedVariant{Width: variant.Width, Height: variant.Height}
		}
		if ad.Canvas.Container != nil {
			c := *ad.Canvas.Container
			item.Container = &c
		}
		ads = append(ads, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "restaurant_id": restaurantID, "ads": ads})
}

// handleAdsPluginJS serves GET /public/ads-plugin.js — the embeddable script.
// Usage on any external site:
//
//	<script src="https://<backend>/api/public/ads-plugin.js" data-restaurant="1" data-mode="overlay" async></script>
//
// data-mode: "overlay" (modal popover, default) or "inline" (injects the iframe
// where the script tag sits). Overlay close is remembered per ad for 24h.
func (s *Server) handleAdsPluginJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, adsPluginJS)
}

const adsPluginJS = `(function () {
  var script = document.currentScript || (function () {
    var s = document.getElementsByTagName("script");
    return s[s.length - 1];
  })();
  if (!script) return;
  var base = new URL(script.src).origin;
  var restaurant = script.getAttribute("data-restaurant") || "";
  if (!restaurant) return;
  var mode = script.getAttribute("data-mode") || "overlay";

  // Coord id ads_canvas_breakpoints_v1 - same thresholds as the home popover.
  function variantForWidth(vw) {
    if (vw > 0 && vw < 768) return "mobile";
    if (vw > 0 && vw < 1024) return "tablet";
    return "desktop";
  }
  function pickDims(ad) {
    var name = variantForWidth(window.innerWidth);
    if (ad.variants && ad.variants[name]) return ad.variants[name];
    return { width: ad.width, height: ad.height };
  }

  function inject(ad, container) {
    var iframe = document.createElement("iframe");
    iframe.src = base + ad.render_url + "&variant=" + variantForWidth(window.innerWidth);
    iframe.setAttribute("title", ad.name || "Anuncio");
    iframe.style.border = "0";
    iframe.style.width = "100%";
    iframe.style.height = "100%";
    iframe.style.display = "block";
    iframe.setAttribute("loading", "lazy");
    container.appendChild(iframe);
  }

  function showOverlay(ad) {
    var seenKey = "vc-ad-seen-" + ad.id;
    try {
      var seen = parseInt(localStorage.getItem(seenKey) || "0", 10);
      if (seen && Date.now() - seen < 24 * 3600 * 1000) return;
    } catch (e) {}
    var dims = pickDims(ad);
    var maxWidth = (ad.container && ad.container.maxWidth) || dims.width;
    var overlay = document.createElement("div");
    overlay.style.cssText = "position:fixed;inset:0;z-index:99999;background:" + ((ad.container && ad.container.overlay) || "rgba(0,0,0,.72)") + ";display:flex;align-items:center;justify-content:center;padding:18px;box-sizing:border-box;";
    var box = document.createElement("div");
    box.style.cssText = "position:relative;width:100%;max-width:" + Math.min(maxWidth, dims.width) + "px;aspect-ratio:" + dims.width + "/" + dims.height + ";max-height:86vh;border-radius:" + ((ad.container && ad.container.radius) || 12) + "px;overflow:hidden;box-shadow:0 18px 60px rgba(0,0,0,.45);background:#fff;";
    var close = document.createElement("button");
    close.setAttribute("aria-label", "Cerrar anuncio");
    close.textContent = "\u00d7";
    close.style.cssText = "position:absolute;top:8px;right:8px;width:40px;height:40px;border:none;border-radius:50%;background:rgba(0,0,0,.55);color:#fff;font-size:22px;line-height:1;cursor:pointer;z-index:2;";
    close.addEventListener("click", function () {
      try { localStorage.setItem(seenKey, String(Date.now())); } catch (e) {}
      overlay.remove();
    });
    overlay.addEventListener("click", function (ev) { if (ev.target === overlay) close.click(); });
    box.appendChild(close);
    inject(ad, box);
    overlay.appendChild(box);
    document.body.appendChild(overlay);
  }

  fetch(base + "/api/public/ads/embed?restaurant_id=" + encodeURIComponent(restaurant))
    .then(function (r) { return r.json(); })
    .then(function (data) {
      if (!data || !data.ads || !data.ads.length) return;
      var ad = data.ads[0];
      if (mode === "inline") {
        var holder = document.createElement("div");
        var dimsInline = pickDims(ad);
        holder.style.cssText = "width:100%;max-width:" + dimsInline.width + "px;aspect-ratio:" + dimsInline.width + "/" + dimsInline.height + ";";
        script.parentNode.insertBefore(holder, script.nextSibling);
        inject(ad, holder);
      } else {
        if (document.readyState === "loading") {
          document.addEventListener("DOMContentLoaded", function () { showOverlay(ad); });
        } else {
          showOverlay(ad);
        }
      }
    })
    .catch(function () {});
})();
`
