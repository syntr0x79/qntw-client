// Exercise the actual country-confirmation model without a browser dependency.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

(async () => {
  const calls = [];
  const context = vm.createContext({
    console, Intl, Date, setTimeout, clearTimeout, setInterval() {},
    requestAnimationFrame() {},
    document: {addEventListener() {}, querySelector() { return null; }, querySelectorAll() { return []; }},
    window: {webkit: {messageHandlers: {quantway: {async postMessage(message) {
      calls.push(message); return {accepted: true};
    }}}}},
  });
  const source = fs.readFileSync(path.join(__dirname, '../Sources/QuantwayApp/Resources/UI/app.js'), 'utf8');
  vm.runInContext(source.replace(/\nbootstrap\(\);\s*$/, '\n'), context);
  vm.runInContext(`render=()=>{};updateNav=()=>{};updateMetrics=()=>{};
    S.mode='tinc';S.source={id:'account'};S.helperRunning=true;
    S.status='connected';S.country='PL';S.assigned='PL';S.started=123;`, context);
  vm.runInContext(`pickCountry('SE')`, context);
  assert.equal(vm.runInContext(`tincVals().confirm`, context), true);
  await vm.runInContext(`tincVals().doSwitch()`, context);
  assert.deepEqual(calls.map(c => c.action), ['country']);
  assert.deepEqual(JSON.parse(JSON.stringify(calls[0].body)), {country: 'SE'});
  assert.equal(vm.runInContext(`S.helperRunning`, context), true);
  assert.equal(vm.runInContext(`S.status`, context), 'reconnecting');
  assert.equal(vm.runInContext(`S.assigned`, context), 'PL');
  vm.runInContext(`window.sessionEvent({status:'connected',country:'PL',desired_country:'SE'})`, context);
  assert.equal(vm.runInContext(`S.status`, context), 'reconnecting');
  vm.runInContext(`window.sessionEvent({status:'connected',country:'SE',desired_country:'SE'})`, context);
  assert.equal(vm.runInContext(`S.status`, context), 'connected');
  assert.equal(vm.runInContext(`S.started`, context), 123);
  assert.equal(vm.runInContext(`S.assigned`, context), 'SE');
  console.log('Country confirmation retains helper/session and waits for assigned country: passed');
})().catch(error => {console.error(error);process.exitCode = 1;});
