// SPDX-License-Identifier: Apache-2.0
// Renders the Overview charts with uPlot from the JSON document in
// #chart-data. No inline scripts or styles, no network access, no eval.
(function () {
  'use strict';

  var HEIGHT = 240;
  var PALETTE_LIGHT = ['#2f6fed', '#c47a00', '#14876a', '#b8368a', '#7a5af8', '#8a6d00', '#0e8fa8', '#c0392b'];
  var PALETTE_DARK = ['#5b94ff', '#e0a030', '#3fc09b', '#e268b4', '#a28bff', '#e6c84a', '#42b8d0', '#ee6f60'];
  var FORMATS = {
    cost: function (v) { return v == null ? '-' : '$' + v.toFixed(v >= 1 ? 2 : 6); },
    tokens: function (v) { return v == null ? '-' : Math.round(v).toLocaleString('en-US'); },
    p95: function (v) { return v == null ? '-' : v.toFixed(v >= 100 ? 0 : 1) + ' ms'; }
  };

  var charts = [];

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function destroyAll() {
    charts.forEach(function (c) { c.plot.destroy(); c.observer.disconnect(); });
    charts = [];
  }

  function palette() {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? PALETTE_DARK : PALETTE_LIGHT;
  }

  function options(box, doc, metric) {
    var fg = cssVar('--fg');
    var grid = cssVar('--grid');
    var colors = palette();
    var fmt = FORMATS[metric];
    var axis = { stroke: fg, grid: { stroke: grid, width: 1 }, ticks: { stroke: grid, width: 1 } };
    var series = [{}];
    doc.models.forEach(function (name, i) {
      series.push({
        label: name || '(unknown)',
        stroke: colors[i % colors.length],
        width: 2,
        spanGaps: true,
        points: { show: doc.x.length < 40 },
        value: function (_u, v) { return fmt(v); }
      });
    });
    return {
      width: Math.max(box.clientWidth, 280),
      height: HEIGHT,
      tzDate: function (ts) { return uPlot.tzDate(new Date(ts * 1000), 'UTC'); },
      legend: { live: true },
      cursor: { drag: { x: false, y: false } },
      scales: { x: { time: true } },
      series: series,
      axes: [axis, Object.assign({ size: 70, values: function (_u, vals) { return vals.map(fmt); } }, axis)]
    };
  }

  function render() {
    var el = document.getElementById('chart-data');
    if (!el || typeof uPlot === 'undefined') { return; }
    var doc;
    try { doc = JSON.parse(el.textContent); } catch (e) { return; }
    destroyAll();
    document.querySelectorAll('[data-chart]').forEach(function (box) {
      var metric = box.getAttribute('data-chart');
      box.textContent = '';
      if (!doc.models.length || !doc.x.length || !doc[metric]) {
        box.textContent = 'No data in this range.';
        return;
      }
      var plot = new uPlot(options(box, doc, metric), [doc.x].concat(doc[metric]), box);
      var observer = new ResizeObserver(function () {
        plot.setSize({ width: Math.max(box.clientWidth, 280), height: HEIGHT });
      });
      observer.observe(box);
      charts.push({ plot: plot, observer: observer });
    });
  }

  document.addEventListener('DOMContentLoaded', render);
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', render);
})();
