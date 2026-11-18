import {expect,it} from 'vitest';
import {changePreset,directCount,presetCount} from '../lib/presets';
it('manual rule and two independent overlapping sets',() => {
 const value='music.apple.com';
 const a={label:'Apple Music',values:[value,'AS714']};
 const b={label:'Second',values:[value]};
 const manual=[{value,enabled:true}];
 expect(presetCount(manual,a)).toBe(0);
 const both=changePreset(changePreset(manual,a),b);
 expect(directCount(both)).toBe(3);
 expect(changePreset(changePreset(both,a),b)).toEqual(manual);
 expect(presetCount(both.filter(e=>e.value!=='AS714'),a)).toBe(1);
});
