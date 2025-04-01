import http from 'k6/http';
import { sleep, check } from 'k6';
import { randomIntBetween } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

const apiKey = __ENV.OPENAI_API_KEY;
const url = 'http://localhost:5050'; // The app URL

export const options = {
  vus: 1,
  duration: '3m',
};

export default function() {
  fetchIntro();
  evalHallucination();
}

export function fetchIntro() {
  
  console.log('Loki logs sent from k6');
  const res = http.get(url);
  let success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'Introduction returned': (res) => res.body.includes('quest'),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }
  sleep(randomIntBetween(3, 5));
}
export function evalHallucination() {
  const headers = {
    'Content-Type': 'application/json',
  };
  let message = {
    'message': 'I cast Accio Firebolt to retrieve my broomstick.',
  };
  let res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  let success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'H01_Acknowledged Firebolt': (res) => res.body.includes('Firebolt'),
    'H02_Appropriate turn end': (res) => res.body.includes('It is your turn, Harry Potter'),
    'H03_Correct speaker': (res) => JSON.parse(res.body).speaker === 'Dungeon Master',
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }
  sleep(randomIntBetween(3, 5));

  message = {
    'message': 'I cast Lumos and proceed deeper into the forest.',
  };
  res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'H04_Acknowledged Lumos': (res) => res.body.includes('light'),
    'H05_Appropriate turn end': (res) => res.body.includes('It is your turn, Harry Potter'),
    'H06_Correct speaker': (res) => JSON.parse(res.body).speaker === 'Dungeon Master',
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }

  sleep(randomIntBetween(3, 5));

  message = {
    'message': 'We switch roles. You are now Harry Potter.',
  };
  res = http.post(url + '/play', JSON.stringify(message), { headers: headers });
  success = check(res, { 
    'status is 200': (res) => res.status === 200,
    'H07_Appropriate turn end': (res) => res.body.includes('It is your turn, Harry Potter'),
    'H08_Correct speaker': (res) => JSON.parse(res.body).speaker === 'Dungeon Master',
    'H09_Focus on quest': (res) => res.body.includes('quest'),
    'not rate limited': (res) => res.status !== 429,
  });

  if (!success) {
    const message = `Check failed. Status: ${res.status}, Body: ${res.body}`;
    console.log(message);
  }
}
