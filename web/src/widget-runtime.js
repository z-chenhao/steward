// These libraries execute inside the opaque-origin frame, never in the Host.
// Bundle locally: generated answers do not need CDN access or import maps.
import * as d3 from 'd3';
import * as dagre from '@dagrejs/dagre';
import * as three from 'three';
import {SVGRenderer} from 'three/addons/renderers/SVGRenderer.js';
import {feature} from 'topojson-client';
import atlas from 'world-atlas/countries-110m.json';

// A renderer's unconfigured white clear color must follow the same theme as
// its frame. Explicit clear colors and scene backgrounds keep Three semantics.
class ThemedSVGRenderer extends SVGRenderer {
  constructor() {
    super();
    const render=this.render.bind(this),clear=this.setClearColor.bind(this);
    let explicit=false,sceneBackground=null;
    const theme=()=>{
      if(explicit||sceneBackground)return;
      const color=getComputedStyle(document.body||document.documentElement).backgroundColor;
      if(color&&color!=='rgba(0, 0, 0, 0)')clear(new three.Color(color));
      this.domElement.style.backgroundColor=color||'transparent';
    };
    this.setClearColor=color=>{explicit=true;clear(color);};
    this.render=(scene,camera)=>{sceneBackground=scene.background;theme();render(scene,camera);};
    new MutationObserver(theme).observe(document.documentElement,{attributes:true,attributeFilter:['data-widget-theme','style']});
  }
}

Object.defineProperties(window, {
  d3: {value: Object.freeze(d3)},
  dagre: {value: Object.freeze(dagre)},
  THREE: {value: Object.freeze({...three, SVGRenderer:ThemedSVGRenderer})},
  // Private frame helpers for the actual centered SVGRenderer coordinate
  // system. Geometry, axis conventions and label placement remain caller data.
  StewardSVG: {value:Object.freeze({
    resize(renderer,camera,element) {
      const width=element.clientWidth,height=element.clientHeight;
      if(width<=0||height<=0)return null;
      renderer.setSize(width,height);
      if(camera.isPerspectiveCamera){camera.aspect=width/height;camera.updateProjectionMatrix();}
      return {width,height};
    },
    project(point,camera,renderer) {
      const position=point.clone().project(camera),size=renderer.getSize();
      return {x:position.x*size.width/2,y:-position.y*size.height/2};
    },
  })},
  GEO: {value: Object.freeze({
    countries: feature(atlas, atlas.objects.countries),
    source: 'Natural Earth 4.1.0 / world-atlas 2.0.2 · 1:110m',
  })},
});
